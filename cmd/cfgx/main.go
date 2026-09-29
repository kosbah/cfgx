package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the CLI with args and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	cmd := newRootCmd(stdout, stderr)
	cmd.SetArgs(args)
	return exitCode(cmd.Execute(), stderr)
}

// exitError carries a specific exit code. If Err is nil the failure has
// already been reported to the user; otherwise main prints it.
type exitError struct {
	Code int
	Err  error
}

func (e *exitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *exitError) Unwrap() error { return e.Err }

// exitCode maps an error returned by a command to an exit code, printing it
// to stderr unless it has already been reported.
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.Err != nil {
			fmt.Fprintf(stderr, "✗ %v\n", ee.Err)
		}
		return ee.Code
	}
	fmt.Fprintf(stderr, "✗ %v\n", err)
	return 1
}
