package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/mbarboss/whyfail/internal/doctor"
)

// Doctor prints one line per check, labeled so the result reads without
// color, with the fix indented below each failure.
func Doctor(w io.Writer, results []doctor.Result) error {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "%-4s  %s\n", r.Status, oneLine(r.Title))
		if r.Fix != "" {
			fmt.Fprintf(&b, "      Fix: %s\n", oneLine(r.Fix))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
