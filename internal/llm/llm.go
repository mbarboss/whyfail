// Package llm defines the port through which whyfail asks a language model to
// explain a failure, and the validation applied to every answer.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Errors returned by Explainer implementations, wrapped with context.
var (
	ErrUnreachable       = errors.New("model server unreachable")
	ErrModelNotFound     = errors.New("model not found")
	ErrTimeout           = errors.New("timed out waiting for the model")
	ErrMalformedResponse = errors.New("malformed model response")
)

// Request is a fully built prompt.
type Request struct {
	System string
	User   string
	// Schema is the JSON Schema the answer must follow.
	Schema json.RawMessage
}

// Fix is one suggested command.
type Fix struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// Explanation is the structured answer. Its content comes from the model and
// is untrusted until validated and sanitized.
type Explanation struct {
	Cause       string `json:"cause"`
	Explanation string `json:"explanation"`
	Fixes       []Fix  `json:"fixes"`
}

// Explainer asks a model to explain a failure.
type Explainer interface {
	Explain(ctx context.Context, req Request) (Explanation, error)
}

// Answer size limits. They keep a misbehaving model from flooding the terminal.
const (
	MaxFixes          = 3
	MaxCauseLen       = 500
	MaxExplanationLen = 2000
	MaxCommandLen     = 500
	MaxDescriptionLen = 500
)

// Validate checks that e is complete and within the size limits. It returns an
// error wrapping ErrMalformedResponse otherwise.
func Validate(e Explanation) error {
	if err := checkText("cause", e.Cause, MaxCauseLen); err != nil {
		return err
	}
	if err := checkText("explanation", e.Explanation, MaxExplanationLen); err != nil {
		return err
	}
	if len(e.Fixes) == 0 || len(e.Fixes) > MaxFixes {
		return fmt.Errorf("%w: got %d fixes, want 1 to %d", ErrMalformedResponse, len(e.Fixes), MaxFixes)
	}
	for i, f := range e.Fixes {
		if err := checkText(fmt.Sprintf("fix %d command", i+1), f.Command, MaxCommandLen); err != nil {
			return err
		}
		// A command that spans lines can hide a second command below the one
		// the user reads.
		if strings.ContainsAny(f.Command, "\r\n") {
			return fmt.Errorf("%w: fix %d command spans several lines", ErrMalformedResponse, i+1)
		}
		if utf8.RuneCountInString(f.Description) > MaxDescriptionLen {
			return fmt.Errorf("%w: fix %d description is too long", ErrMalformedResponse, i+1)
		}
	}
	return nil
}

func checkText(field, s string, maxLen int) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%w: %s is empty", ErrMalformedResponse, field)
	}
	if utf8.RuneCountInString(s) > maxLen {
		return fmt.Errorf("%w: %s is too long", ErrMalformedResponse, field)
	}
	return nil
}
