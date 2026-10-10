package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mbarboss/whyfail/internal/llm"
)

const answer = `{"cause":"No upstream.","explanation":"Git does not know where to push.","fixes":[{"command":"git push -u origin main","description":"Set upstream."}]}`

var testReq = llm.Request{System: "sys", User: "usr", Schema: json.RawMessage(`{"type":"object"}`)}

// chatReply builds an /api/chat non-streaming response body.
func chatReply(content, doneReason string) string {
	b, _ := json.Marshal(map[string]any{
		"model":       "m",
		"message":     map[string]string{"role": "assistant", "content": content},
		"done":        true,
		"done_reason": doneReason,
		"eval_count":  42,
	})
	return string(b)
}

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return New(u, "gemma4:e4b", srv.Client())
}

func TestExplainSendsExpectedRequest(t *testing.T) {
	var got map[string]any
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fmt.Fprint(w, chatReply(answer, "stop"))
	})

	if _, err := c.Explain(context.Background(), testReq); err != nil {
		t.Fatalf("Explain: %v", err)
	}

	if got["model"] != "gemma4:e4b" || got["stream"] != false || got["think"] != false {
		t.Errorf("model/stream/think = %v/%v/%v", got["model"], got["stream"], got["think"])
	}
	if f, ok := got["format"].(map[string]any); !ok || f["type"] != "object" {
		t.Errorf("format = %v, want the schema object", got["format"])
	}
	opts, _ := got["options"].(map[string]any)
	if opts["temperature"] != float64(0) || opts["num_predict"] != float64(maxPredict) {
		t.Errorf("options = %v", opts)
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
	sys, _ := msgs[0].(map[string]any)
	usr, _ := msgs[1].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "sys" || usr["role"] != "user" || usr["content"] != "usr" {
		t.Errorf("messages = %v", msgs)
	}
}

func TestExplainDecodesAnswer(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, chatReply(answer, "stop"))
	})

	got, err := c.Explain(context.Background(), testReq)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	if got.Cause != "No upstream." || len(got.Fixes) != 1 || got.Fixes[0].Command != "git push -u origin main" {
		t.Errorf("got %+v", got)
	}
}

func TestExplainMapsModelNotFound(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model \"gemma4:e4b\" not found, try pulling it first"}`)
	})

	_, err := c.Explain(context.Background(), testReq)

	if !errors.Is(err, llm.ErrModelNotFound) {
		t.Errorf("err = %v, want ErrModelNotFound", err)
	}
}

func TestExplainReportsServerError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"out of memory"}`)
	})

	_, err := c.Explain(context.Background(), testReq)

	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "out of memory") {
		t.Errorf("err = %v, want status and server message", err)
	}
}

func TestExplainRejectsMalformedAnswers(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"body not JSON", "<html>proxy error</html>"},
		{"content not JSON", chatReply("Sure! The cause is...", "stop")},
		{"content with unknown field", chatReply(`{"cause":"a","explanation":"b","fixes":[{"command":"c","description":"d"}],"run":true}`, "stop")},
		{"content fails validation", chatReply(`{"cause":"a","explanation":"b","fixes":[]}`, "stop")},
		{"content with trailing data", chatReply(answer+`{"x":1}`, "stop")},
		{"truncated by token limit", chatReply(`{"cause":"a","expl`, "length")},
		{"missing message", `{"done":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tt.body) })

			_, err := c.Explain(context.Background(), testReq)

			if !errors.Is(err, llm.ErrMalformedResponse) {
				t.Errorf("err = %v, want ErrMalformedResponse", err)
			}
		})
	}
}

func TestExplainLimitsResponseSize(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, chatReply(strings.Repeat("a", maxResponseBytes), "stop"))
	})

	_, err := c.Explain(context.Background(), testReq)

	if !errors.Is(err, llm.ErrMalformedResponse) {
		t.Errorf("err = %v, want ErrMalformedResponse", err)
	}
}

func TestExplainMapsUnreachable(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	// Close it so nothing listens on the address any more.
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	c := New(&url.URL{Scheme: "http", Host: addr}, "m", &http.Client{})
	_, err = c.Explain(context.Background(), testReq)

	if !errors.Is(err, llm.ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

// hangingClient returns a client whose server never answers. The handler is
// released before the server closes, since cleanups run last-in first-out.
func hangingClient(t *testing.T) *Client {
	t.Helper()
	release := make(chan struct{})
	c := newClient(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	return c
}

func TestExplainMapsDeadlineToTimeout(t *testing.T) {
	c := hangingClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Explain(ctx, testReq)

	if !errors.Is(err, llm.ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
}

func TestExplainKeepsCancellation(t *testing.T) {
	c := hangingClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	_, err := c.Explain(ctx, testReq)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
