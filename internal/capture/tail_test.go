package capture

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

func TestReadTailKeepsShortInputUntouched(t *testing.T) {
	in := "make: *** [all] Error 1\n"

	got, err := ReadTail(strings.NewReader(in), DefaultLimits)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if got.Text != in || got.Truncated {
		t.Errorf("got %+v, want %q untruncated", got, in)
	}
}

func TestReadTailKeepsLastLines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}

	got, err := ReadTail(strings.NewReader(b.String()), Limits{MaxBytes: 1 << 10, MaxLines: 3})
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if want := "line 8\nline 9\nline 10\n"; got.Text != want {
		t.Errorf("Text = %q, want %q", got.Text, want)
	}
	if !got.Truncated {
		t.Error("Truncated = false")
	}
}

func TestReadTailLineLimitCountsLastLineWithoutNewline(t *testing.T) {
	got, err := ReadTail(strings.NewReader("a\nb\nc"), Limits{MaxBytes: 1 << 10, MaxLines: 2})
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if got.Text != "b\nc" {
		t.Errorf("Text = %q", got.Text)
	}
}

func TestReadTailKeepsLastBytesFromLineBoundary(t *testing.T) {
	in := strings.Repeat("x", 100) + "\n" + "tail error\n"

	got, err := ReadTail(strings.NewReader(in), Limits{MaxBytes: 20, MaxLines: 100})
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if got.Text != "tail error\n" || !got.Truncated {
		t.Errorf("got %+v", got)
	}
}

func TestReadTailCutsLongSingleLine(t *testing.T) {
	in := strings.Repeat("a", 50) + "END"

	got, err := ReadTail(strings.NewReader(in), Limits{MaxBytes: 10, MaxLines: 100})
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if len(got.Text) != 10 || !strings.HasSuffix(got.Text, "END") || !got.Truncated {
		t.Errorf("got %+v", got)
	}
}

func TestReadTailNeverSplitsARune(t *testing.T) {
	in := strings.Repeat("é", 20) // two bytes each

	got, err := ReadTail(strings.NewReader(in), Limits{MaxBytes: 7, MaxLines: 100})
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if !utf8.ValidString(got.Text) || got.Text != "ééé" {
		t.Errorf("Text = %q", got.Text)
	}
}

func TestReadTailReplacesInvalidUTF8(t *testing.T) {
	got, err := ReadTail(strings.NewReader("bad \xff\xfe byte\n"), DefaultLimits)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if !utf8.ValidString(got.Text) || !strings.Contains(got.Text, "byte") {
		t.Errorf("Text = %q", got.Text)
	}
}

func TestReadTailNormalizesCRLF(t *testing.T) {
	got, err := ReadTail(strings.NewReader("one\r\ntwo\r\n"), DefaultLimits)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if got.Text != "one\ntwo\n" {
		t.Errorf("Text = %q", got.Text)
	}
}

func TestReadTailBoundedOnLargeInput(t *testing.T) {
	// 8 MiB of noise followed by the real error, read in small chunks.
	big := strings.Repeat(strings.Repeat("n", 1023)+"\n", 8<<10) + "panic: boom\n"

	got, err := ReadTail(iotest.HalfReader(strings.NewReader(big)), DefaultLimits)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}

	if len(got.Text) > DefaultLimits.MaxBytes || !strings.HasSuffix(got.Text, "panic: boom\n") || !got.Truncated {
		t.Errorf("len=%d truncated=%v suffix ok=%v", len(got.Text), got.Truncated, strings.HasSuffix(got.Text, "panic: boom\n"))
	}
}

func TestReadTailEmptyInput(t *testing.T) {
	for _, in := range []string{"", "  \n\t\r\n"} {
		_, err := ReadTail(strings.NewReader(in), DefaultLimits)

		if !errors.Is(err, ErrEmpty) {
			t.Errorf("ReadTail(%q) err = %v, want ErrEmpty", in, err)
		}
	}
}

func TestReadTailPropagatesReadError(t *testing.T) {
	boom := errors.New("boom")

	_, err := ReadTail(io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(boom)), DefaultLimits)

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want wrapped boom", err)
	}
}

func TestReadTailRejectsInvalidLimits(t *testing.T) {
	for _, l := range []Limits{{MaxBytes: 0, MaxLines: 1}, {MaxBytes: 1, MaxLines: 0}, {MaxBytes: -1, MaxLines: -1}} {
		if _, err := ReadTail(strings.NewReader("x"), l); err == nil {
			t.Errorf("ReadTail with %+v: want error", l)
		}
	}
}
