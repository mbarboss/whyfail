package sysinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func parentName() (string, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(os.Getppid()), "comm"))
	if err != nil {
		return "", fmt.Errorf("sysinfo: read parent process name: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}
