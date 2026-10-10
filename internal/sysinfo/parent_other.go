//go:build !linux && !windows

package sysinfo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// psTimeout bounds the ps call; detection must never hold up the answer.
const psTimeout = 2 * time.Second

// parentName asks ps, which ships with macOS and the BSDs, because reading
// another process's name there needs sysctl structures that the standard
// library does not expose.
func parentName() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ps", "-o", "comm=", "-p", strconv.Itoa(os.Getppid())).Output() //nolint:gosec // G204: fixed binary; the only variable is our parent PID
	if err != nil {
		return "", fmt.Errorf("sysinfo: ps: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
