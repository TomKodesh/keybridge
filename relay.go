package main

// This file is new in KeyBridge (not part of the original WinCryptSSHAgent).
// It gives the binary a second mode of operation: relaying stdin/stdout to
// a Windows named pipe, so it can be invoked from WSL (via `socat`) the same
// way jstarks/npiperelay is -- e.g. to reach this binary's own SSH-agent
// pipe, or any other named pipe (Docker Desktop, a Windows MySQL service,
// etc). It does not share code with npiperelay: it's written against
// go-winio, already a dependency of this project, rather than raw syscalls.

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
// failed attempts (pipe not created yet) are retried by the caller; this
// timeout only guards against a single attempt hanging.
const dialTimeoutPerAttempt = 5 * time.Second

// pollInterval is how long to wait between attempts when polling for a
// pipe that doesn't exist yet.
const pollInterval = 200 * time.Millisecond

func runRelay(args []string) {
	fs := flag.NewFlagSet("relay", flag.ExitOnError)
	poll := fs.Bool("p", false, "poll until the named pipe exists")
	closeWrite := fs.Bool("s", false, "send a 0-byte message to the pipe after EOF on stdin")
	closeOnPipeEOF := fs.Bool("ep", false, "terminate on EOF reading from the pipe, even if there is more data to write")
	closeOnStdinEOF := fs.Bool("ei", false, "terminate on EOF reading from stdin, even if there is more data to write")
	verbose := fs.Bool("v", false, "verbose output on stderr")
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

		if *closeOnStdinEOF {
			os.Exit(0)
		}

		if *closeWrite {
			// A zero-byte write on a message pipe indicates that no more
			// data is coming.
			_, _ = conn.Write(nil)
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
	<-stdinDone
}

func dialPipeWithPoll(path string, poll bool) (net.Conn, error) {
	timeout := dialTimeoutPerAttempt
	for {
		conn, err := winio.DialPipe(path, &timeout)
		if err == nil {
			return conn, nil
		}
		if poll && os.IsNotExist(err) {
			time.Sleep(pollInterval)
			continue
		}
		return nil, err
	}
}
