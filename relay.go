package main

// This file is new in KeyBridge (not part of the original WinCryptSSHAgent).
// It gives the binary a second mode of operation: relaying stdin/stdout to
// a Windows named pipe, so it can be invoked from WSL (via `socat`) the same
// way jstarks/npiperelay is -- e.g. to reach this binary's own SSH-agent
// pipe, or any other named pipe (Docker Desktop, a Windows MySQL service,
// etc). It does not share code with npiperelay: it's written against
// go-winio, already a dependency of this project, rather than raw syscalls.
//
// Known limitation, inherited from upstream npiperelay's own pattern rather
// than introduced here: every process-ending path below (log.Fatalln, and
// the flag-driven os.Exit(0) calls under -ei/-ep) tears down the whole
// process immediately. If that happens in one goroutine at the exact moment
// the other is mid-copy, already-read data that hasn't been written out yet
// is lost rather than flushed -- not just on the error paths, but on the
// -ei/-ep fast-exit paths too, since -ei and -ep intentionally mean "exit
// now, even if there's more to write" and os.Exit has no way to let an
// in-flight write finish first. Fixing this properly would mean replacing
// the fail-fast os.Exit error model with coordinated shutdown across both
// goroutines, which is more machinery than a short-lived CLI relay
// warrants for what is a narrow, timing-dependent edge case.

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialTimeoutPerAttempt bounds a single connection attempt. In poll mode,
// failed attempts (pipe not created yet, or busy for the whole attempt) are
// retried by the caller; this timeout only guards against one attempt
// hanging.
const dialTimeoutPerAttempt = 5 * time.Second

// pollInterval is how long to wait between attempts when polling for a pipe
// that doesn't exist yet. It also applies when a pipe exists but stayed
// busy for a full dialTimeoutPerAttempt: go-winio already retries
// ERROR_PIPE_BUSY internally every 10ms for the duration of one attempt, so
// against a persistently busy pipe the real end-to-end retry cadence is
// roughly dialTimeoutPerAttempt+pollInterval (~5.2s), not pollInterval --
// this constant only paces the outer loop between those inner attempts.
const pollInterval = 200 * time.Millisecond

// stdinDrainTimeout bounds how long the process waits for the stdin->pipe
// side to finish on its own after the pipe->stdout side has ended (and -ep
// wasn't given). Without this, a pipe that dies while stdin is idle (e.g. a
// held-open SSH connection with nothing left to send) would hang the
// process forever, since go-winio can't tell us the pipe actually broke
// rather than just pausing -- see the comment above the pipe->stdout copy
// below. The tradeoff: if stdin is instead still *actively* sending real
// data more than stdinDrainTimeout after the pipe side ended -- plausible
// for a generic relay target with asymmetric traffic (e.g. a slow upload
// against a service that acknowledges early), though not for the
// SSH-agent-forwarding case this binary mainly exists for, since that's a
// synchronous request/response -- this exits before that data is delivered
// instead of hanging. If a specific pipe's traffic pattern needs one
// behavior guaranteed over the other, -ep or -ei make the choice explicit
// instead of relying on this timeout.
const stdinDrainTimeout = 5 * time.Second

// closeWriter is implemented by go-winio's message-mode pipe connections.
// It matters because a plain zero-length Write() is not a usable
// substitute: per go-winio's own PipeConfig.MessageMode doc comment,
// "zero-byte writes are only transferred to the reader ... when the pipe is
// in message mode" -- on a byte-mode pipe, a zero-length WriteFile reaches
// the real syscall but is never observed by the reader at all, so there is
// no way to signal end-of-data on a byte-mode pipe short of closing the
// whole connection. That includes this binary's own SSH-agent pipe
// (app/pipe.go listens with a zero-value PipeConfig, i.e. byte mode), so
// -s has no effect there -- see the warning logged below when this
// assertion fails.
type closeWriter interface {
	CloseWrite() error
}

func runRelay(args []string) {
	fs := flag.NewFlagSet("relay", flag.ExitOnError)
	poll := fs.Bool("p", false, "keep retrying instead of failing immediately if the pipe doesn't exist yet")
	closeWrite := fs.Bool("s", false, "once stdin closes, signal end-of-data to the pipe (message-mode pipes only -- see docs)")
	closeOnPipeEOF := fs.Bool("ep", false, "exit as soon as the pipe side closes, without waiting on the stdin side")
	closeOnStdinEOF := fs.Bool("ei", false, "exit as soon as stdin closes, without waiting on the pipe side")
	verbose := fs.Bool("v", false, "log connection and shutdown events to stderr")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: keybridge relay [-p] [-s] [-ep] [-ei] [-v] <named pipe path>")
		fmt.Fprintln(os.Stderr, "flags must come before the pipe path -- Go's flag parser stops at the first non-flag argument")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		if fs.NArg() > 1 {
			for _, extra := range fs.Args()[1:] {
				if strings.HasPrefix(extra, "-") {
					fmt.Fprintf(os.Stderr, "%q was given after the pipe path, so it was treated as part of the path instead of a flag.\n", extra)
					break
				}
			}
		}
		fs.Usage()
		os.Exit(1)
	}
	pipePath := fs.Arg(0)

	logf := func(format string, v ...interface{}) {
		if *verbose {
			log.Printf(format, v...)
		}
	}

	logf("connecting to %s", pipePath)
	conn, err := dialPipeWithPoll(pipePath, *poll)
	if err != nil {
		log.Fatalln(err)
	}
	logf("connected")

	stdinDone := make(chan struct{})
	go func() {
		copyOrFatal(conn, os.Stdin, "copy from stdin to pipe failed")
		logf("copy from stdin to pipe finished")

		if *closeWrite {
			if cw, ok := conn.(closeWriter); ok {
				if err := cw.CloseWrite(); err != nil {
					logf("close-write failed: %v", err)
				}
			} else {
				// See closeWriter's doc comment: this pipe is byte-mode, so
				// there is no way to deliver an end-of-data signal short of
				// closing the connection outright. Reported unconditionally
				// (not gated behind -v) since -s silently doing nothing is
				// exactly the failure mode worth surfacing.
				fmt.Fprintln(os.Stderr, "-s has no effect on this pipe: it's byte-mode, which has no end-of-data signal at the Win32 API level")
			}
		}

		// Checked after -s's write (not before): closeWrite still needs to
		// run even when -ei is also set, or combining the two flags would
		// silently drop the close-write signal.
		if *closeOnStdinEOF {
			os.Exit(0)
		}

		close(stdinDone)
	}()

	// go-winio surfaces both a clean 0-byte "no more data" write from the
	// remote and an actual broken pipe as io.EOF (io.Copy returns a nil
	// error in both cases) -- unlike the raw Windows API, it doesn't
	// distinguish them. So, unlike upstream npiperelay, we can't tell here
	// whether the pipe merely signaled "done for now" or actually died.
	// Treat both the same: report the pipe direction finished, honor -ep,
	// and otherwise wait on the stdin copy to finish on its own (bounded by
	// stdinDrainTimeout) rather than polling the possibly-already-dead pipe
	// for a break we can no longer distinguish.
	copyOrFatal(os.Stdout, conn, "copy from pipe to stdout failed")
	logf("copy from pipe to stdout finished")

	if *closeOnPipeEOF {
		os.Exit(0)
	}

	if err := os.Stdout.Close(); err != nil {
		logf("closing stdout failed: %v", err)
	}

	select {
	case <-stdinDone:
	case <-time.After(stdinDrainTimeout):
		// The pipe side ended and stdin still hasn't closed on its own
		// after a generous grace period -- most likely the pipe actually
		// broke (rather than cleanly signaling end-of-data) while stdin
		// was idle waiting on something upstream that will never arrive.
		// Exit rather than hang forever; see stdinDrainTimeout's comment
		// for the tradeoff this makes.
		logf("stdin side still open %s after the pipe closed; exiting anyway", stdinDrainTimeout)
	}
}

// copyOrFatal is shared by both directions of the relay so the "log and
// exit the whole process" error policy can't drift between them.
func copyOrFatal(dst io.Writer, src io.Reader, failMsg string) {
	if _, err := io.Copy(dst, src); err != nil {
		log.Fatalln(failMsg+":", err)
	}
}

func dialPipeWithPoll(path string, poll bool) (net.Conn, error) {
	timeout := dialTimeoutPerAttempt
	for {
		conn, err := winio.DialPipe(path, &timeout)
		if err == nil {
			return conn, nil
		}
		// os.IsNotExist: the pipe hasn't been created yet.
		// winio.ErrTimeout: the pipe exists but stayed busy (all instances
		// taken) for the whole attempt -- see pollInterval's comment for
		// why the effective retry cadence is coarser than it looks here.
		if poll && (os.IsNotExist(err) || err == winio.ErrTimeout) {
			time.Sleep(pollInterval)
			continue
		}
		return nil, err
	}
}
