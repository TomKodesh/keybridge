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
// than introduced here: both goroutines below call log.Fatalln on a copy
// error, which calls os.Exit and terminates the whole process immediately.
// If that happens in one goroutine at the exact moment the other is
// mid-copy, already-read data that hasn't been written out yet is lost
// rather than flushed. Fixing this properly would mean replacing the
// fail-fast os.Exit error model with coordinated shutdown across both
// goroutines, which is more machinery than a short-lived CLI relay
// warrants for what is a narrow, timing-dependent edge case.

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialTimeoutPerAttempt bounds a single connection attempt. In poll mode,
// failed attempts (pipe not created yet, or busy for the whole attempt) are
// retried by the caller; this timeout only guards against one attempt
// hanging.
const dialTimeoutPerAttempt = 5 * time.Second

// pollInterval is how long to wait between attempts when polling for a
// pipe that doesn't exist yet, or that has stayed busy for a full attempt.
const pollInterval = 200 * time.Millisecond

// stdinDrainTimeout bounds how long the process waits for the stdin->pipe
// side to finish on its own after the pipe->stdout side has ended (and
// -ep wasn't given). Without this, a pipe that dies while stdin is idle
// (e.g. a held-open SSH connection with nothing left to send) would hang
// the process forever, since go-winio can't tell us the pipe actually
// broke rather than just pausing -- see the comment above the pipe->stdout
// copy below.
const stdinDrainTimeout = 5 * time.Second

// closeWriter is implemented by go-winio's message-mode pipe connections.
// Plain byte-mode pipes don't implement it -- Write(nil) already reaches
// the real WriteFile syscall for those, so the fallback below is correct
// either way.
type closeWriter interface {
	CloseWrite() error
}

func runRelay(args []string) {
	fs := flag.NewFlagSet("relay", flag.ExitOnError)
	poll := fs.Bool("p", false, "keep retrying instead of failing immediately if the pipe doesn't exist yet")
	closeWrite := fs.Bool("s", false, "once stdin closes, write a zero-length message to the pipe to mark end-of-data")
	closeOnPipeEOF := fs.Bool("ep", false, "exit as soon as the pipe side closes, without waiting on the stdin side")
	closeOnStdinEOF := fs.Bool("ei", false, "exit as soon as stdin closes, without waiting on the pipe side")
	verbose := fs.Bool("v", false, "log connection and shutdown events to stderr")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: keybridge relay [-p] [-s] [-ep] [-ei] [-v] <named pipe path>")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
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
		_, err := io.Copy(conn, os.Stdin)
		if err != nil {
			log.Fatalln("copy from stdin to pipe failed:", err)
		}
		logf("copy from stdin to pipe finished")

		if *closeWrite {
			// On a message-mode pipe, go-winio treats Write(nil) as a
			// no-op -- it reserves zero-length writes internally for its
			// own CloseWrite() implementation, so a zero-length Write()
			// here would silently do nothing. Byte-mode pipes have no
			// CloseWrite() method (Write(nil) already reaches the real
			// WriteFile syscall for those), so the type assertion falling
			// through to the plain Write is correct there too.
			if cw, ok := conn.(closeWriter); ok {
				if err := cw.CloseWrite(); err != nil {
					logf("close-write failed: %v", err)
				}
			} else {
				_, _ = conn.Write(nil)
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
	// and otherwise wait on the stdin copy to finish on its own rather than
	// polling the (possibly already-dead) pipe for a break we can no
	// longer distinguish.
	_, err = io.Copy(os.Stdout, conn)
	if err != nil {
		log.Fatalln("copy from pipe to stdout failed:", err)
	}
	logf("copy from pipe to stdout finished")

	if *closeOnPipeEOF {
		os.Exit(0)
	}

	os.Stdout.Close()

	select {
	case <-stdinDone:
	case <-time.After(stdinDrainTimeout):
		// The pipe side ended and stdin still hasn't closed on its own
		// after a generous grace period -- most likely the pipe actually
		// broke (rather than cleanly signaling end-of-data) while stdin
		// was idle waiting on something upstream that will never arrive.
		// Exit rather than hang forever; see stdinDrainTimeout's comment.
		logf("stdin side still open %s after the pipe closed; exiting anyway", stdinDrainTimeout)
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
		// taken) for the whole attempt -- go-winio retries ERROR_PIPE_BUSY
		// internally, but only up to dialTimeoutPerAttempt per call, so
		// poll mode needs to keep going here too or a persistently busy
		// pipe gives up instead of "keep retrying" as the flag promises.
		if poll && (os.IsNotExist(err) || err == winio.ErrTimeout) {
			time.Sleep(pollInterval)
			continue
		}
		return nil, err
	}
}
