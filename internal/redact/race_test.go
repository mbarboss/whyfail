//go:build race

package redact

// raceEnabled reports whether the race detector is on; it slows code down
// too much for timing assertions to mean anything.
const raceEnabled = true
