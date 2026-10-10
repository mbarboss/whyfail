package llm

import (
	"errors"
	"strings"
	"testing"
)

func valid() Explanation {
	return Explanation{
		Cause:       "The branch has no upstream.",
		Explanation: "Git does not know where to push.",
		Fixes:       []Fix{{Command: "git push -u origin main", Description: "Set the upstream and push."}},
	}
}

func TestValidateAcceptsCompleteAnswer(t *testing.T) {
	if err := Validate(valid()); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidateRejectsBadAnswers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Explanation)
	}{
		{"empty cause", func(e *Explanation) { e.Cause = "  " }},
		{"empty explanation", func(e *Explanation) { e.Explanation = "" }},
		{"no fixes", func(e *Explanation) { e.Fixes = nil }},
		{"too many fixes", func(e *Explanation) {
			e.Fixes = []Fix{e.Fixes[0], e.Fixes[0], e.Fixes[0], e.Fixes[0]}
		}},
		{"empty command", func(e *Explanation) { e.Fixes[0].Command = " " }},
		{"multi-line command", func(e *Explanation) { e.Fixes[0].Command = "true\nrm -rf ~" }},
		{"carriage return in command", func(e *Explanation) { e.Fixes[0].Command = "echo ok\rrm -rf ~" }},
		{"cause too long", func(e *Explanation) { e.Cause = strings.Repeat("a", MaxCauseLen+1) }},
		{"explanation too long", func(e *Explanation) { e.Explanation = strings.Repeat("a", MaxExplanationLen+1) }},
		{"command too long", func(e *Explanation) { e.Fixes[0].Command = strings.Repeat("a", MaxCommandLen+1) }},
		{"description too long", func(e *Explanation) { e.Fixes[0].Description = strings.Repeat("a", MaxDescriptionLen+1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := valid()
			tt.mutate(&e)

			if err := Validate(e); !errors.Is(err, ErrMalformedResponse) {
				t.Errorf("err = %v, want ErrMalformedResponse", err)
			}
		})
	}
}

func TestValidateCountsCharactersNotBytes(t *testing.T) {
	e := valid()
	e.Cause = strings.Repeat("é", MaxCauseLen)

	if err := Validate(e); err != nil {
		t.Errorf("Validate: %v", err)
	}
}
