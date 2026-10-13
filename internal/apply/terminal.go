package apply

import "io"

// Terminal is the user's console, opened directly so that confirmations work
// in pipe mode, where stdin is the failed command's output.
type Terminal struct {
	In    io.Reader
	Out   io.Writer
	Close func() error
}
