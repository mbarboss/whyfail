//go:build !windows

package apply

import (
	"fmt"
	"os"
)

// OpenTerminal opens the controlling terminal. It fails when whyfail has
// none, as under cron or in CI.
func OpenTerminal() (Terminal, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return Terminal{}, fmt.Errorf("apply: open terminal: %w", err)
	}
	return Terminal{In: tty, Out: tty, Close: tty.Close}, nil
}
