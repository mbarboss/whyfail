// Command whyfail explains why a terminal command failed and suggests a fix,
// using a local LLM through Ollama.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// Exit codes shared by every whyfail mode.
const (
	exitOK    = 0
	exitUsage = 2
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses args and executes the requested mode, returning the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("whyfail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	if *showVersion {
		fmt.Fprintf(stdout, "whyfail %s\n", version)
		return exitOK
	}

	fmt.Fprintln(stderr, "whyfail: no mode implemented yet, run with -help for options")
	return exitUsage
}
