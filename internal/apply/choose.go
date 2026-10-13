package apply

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/mbarboss/whyfail/internal/redact"
)

// Reasons Choose returns without a fix to run.
var (
	ErrSkipped  = errors.New("no fix chosen")
	ErrDeclined = errors.New("fix not confirmed")
	// ErrRedacted means the chosen fix holds a redaction placeholder, so
	// running it would use the placeholder instead of the real value.
	ErrRedacted = errors.New("fix contains a redacted value")
	// ErrPlaceholder means the chosen fix still holds a placeholder such as
	// <branch-name>, which a shell would read as a redirection.
	ErrPlaceholder = errors.New("fix contains a placeholder")
)

// Choose asks which of commands to run and then asks to confirm it, reading
// answers from in and writing the dialog to out. It returns the index of the
// confirmed command. commands must be exactly as displayed, since the
// confirmed one runs as is; shell names the program that runs it, and warn
// returns the warnings for a command (Warnings or WarningsFor).
//
// The default answer is always no. A command with a danger warning needs the
// word "yes", not just "y".
func Choose(ctx context.Context, in io.Reader, out io.Writer, commands []string, shell string, warn func(string) []string) (int, error) {
	r := lineReader{ctx: ctx, r: bufio.NewReader(in)}
	i := 0
	if len(commands) > 1 {
		var err error
		if i, err = pick(r, out, len(commands)); err != nil {
			return 0, interrupted(out, err)
		}
	}
	if redact.HasPlaceholder(commands[i]) {
		return i, fmt.Errorf("%w: fix %d", ErrRedacted, i+1)
	}
	if placeholder.MatchString(commands[i]) {
		return i, fmt.Errorf("%w: fix %d", ErrPlaceholder, i+1)
	}
	return i, interrupted(out, confirm(r, out, commands[i], shell, warn))
}

// pick returns the zero-based index of the fix the user typed. It asks again
// after an answer that is not a fix number.
func pick(r lineReader, out io.Writer, n int) (int, error) {
	for {
		fmt.Fprintf(out, "Run which fix? [1-%d, Enter to skip]: ", n)
		line, err := r.read()
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(out)
			return 0, ErrSkipped
		}
		if err != nil {
			return 0, err
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			return 0, ErrSkipped
		}
		if k, err := strconv.Atoi(answer); err == nil && k >= 1 && k <= n {
			return k - 1, nil
		}
	}
}

func confirm(r lineReader, out io.Writer, command, shell string, warn func(string) []string) error {
	fmt.Fprintf(out, "\n  %s\n", command)
	warnings := warn(command)
	for _, w := range warnings {
		fmt.Fprintf(out, "  Warning: %s.\n", w)
	}

	accepted := []string{"y", "yes"}
	question := fmt.Sprintf("Run it with %s? [y/N]: ", shell)
	if len(warnings) > 0 {
		accepted = []string{"yes"}
		question = fmt.Sprintf(`Type "yes" to run it with %s: `, shell)
	}
	fmt.Fprint(out, question)

	line, err := r.read()
	if errors.Is(err, io.EOF) {
		fmt.Fprintln(out)
		return ErrDeclined
	}
	if err != nil {
		return err
	}
	if !slices.Contains(accepted, strings.ToLower(strings.TrimSpace(line))) {
		return ErrDeclined
	}
	return nil
}

// interrupted ends the prompt line when Ctrl+C stopped the dialog, so the
// next output starts on a line of its own.
func interrupted(out io.Writer, err error) error {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(out)
	}
	return err
}

// lineReader reads answers until ctx is done. On Unix, Ctrl+C reaches
// whyfail as a signal that does not interrupt a terminal read.
type lineReader struct {
	ctx context.Context //nolint:containedctx // scoped to a single dialog
	r   *bufio.Reader
}

// read returns the next line, or io.EOF when the input ends without one. A
// read still blocked when ctx is done is abandoned; the dialog ends there.
func (l lineReader) read() (string, error) {
	if err := l.ctx.Err(); err != nil {
		return "", err
	}
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := l.r.ReadString('\n')
		done <- result{line, err}
	}()

	select {
	case <-l.ctx.Done():
		return "", l.ctx.Err()
	case res := <-done:
		switch {
		case errors.Is(res.err, io.EOF) && res.line != "":
			return res.line, nil
		case errors.Is(res.err, io.EOF):
			return "", io.EOF
		case res.err != nil:
			return "", fmt.Errorf("apply: read answer: %w", res.err)
		}
		return res.line, nil
	}
}
