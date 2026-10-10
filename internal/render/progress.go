package render

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var frames = [...]string{"|", "/", "-", "\\"}

// StartProgress draws a one-line spinner with label on w every interval until
// the returned stop function is called. stop erases the line and waits for the
// spinner to finish writing. Only carriage returns are used, so it works on
// any terminal without escape sequences. Drawing is best effort, like any
// other write to stderr.
func StartProgress(w io.Writer, label string, interval time.Duration) (stop func()) {
	line := label + " " + frames[0]
	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		fmt.Fprint(w, line)
		t := time.NewTicker(interval)
		defer t.Stop()
		for i := 1; ; i++ {
			select {
			case <-done:
				fmt.Fprint(w, "\r"+strings.Repeat(" ", len(line))+"\r")
				return
			case <-t.C:
				fmt.Fprint(w, "\r"+label+" "+frames[i%len(frames)])
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-finished
		})
	}
}
