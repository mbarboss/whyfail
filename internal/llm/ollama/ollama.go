// Package ollama implements llm.Explainer over the Ollama REST API.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/mbarboss/whyfail/internal/llm"
)

const (
	// maxPredict caps generated tokens; a full answer takes about 200.
	maxPredict = 512
	// maxResponseBytes caps the response body read from the server.
	maxResponseBytes = 1 << 20
	// maxErrorBytes caps the part of an error body quoted in errors.
	maxErrorBytes = 200
)

// Client calls the Ollama /api/chat endpoint.
type Client struct {
	endpoint string
	model    string
	http     *http.Client
}

// New returns a Client for the server at base using model. Request deadlines
// come from the context passed to Explain.
func New(base *url.URL, model string, hc *http.Client) *Client {
	return &Client{endpoint: base.JoinPath("api", "chat").String(), model: model, http: hc}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string          `json:"model"`
	Messages []message       `json:"messages"`
	Format   json.RawMessage `json:"format"`
	Stream   bool            `json:"stream"`
	// Think is always sent as false: thinking models otherwise spend the whole
	// token budget reasoning and return empty content.
	Think   bool    `json:"think"`
	Options options `json:"options"`
}

type options struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict"`
}

type chatResponse struct {
	Message    *message `json:"message"`
	DoneReason string   `json:"done_reason"`
}

// Explain implements llm.Explainer.
func (c *Client) Explain(ctx context.Context, req llm.Request) (llm.Explanation, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
		Format:  req.Schema,
		Options: options{Temperature: 0, NumPredict: maxPredict},
	})
	if err != nil {
		return llm.Explanation{}, fmt.Errorf("ollama: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return llm.Explanation{}, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return llm.Explanation{}, transportError(ctx, err)
	}
	// The body is fully read below; a close error cannot change the outcome.
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return llm.Explanation{}, transportError(ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		return llm.Explanation{}, statusError(resp.StatusCode, raw)
	}
	if len(raw) > maxResponseBytes {
		return llm.Explanation{}, fmt.Errorf("ollama: %w: response larger than %d bytes", llm.ErrMalformedResponse, maxResponseBytes)
	}
	return decode(raw)
}

// transportError maps a failed exchange to the llm errors. Cancellation keeps
// its own error so callers can tell an interrupt from a timeout.
func transportError(ctx context.Context, err error) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("ollama: %w", llm.ErrTimeout)
	case errors.Is(ctx.Err(), context.Canceled):
		return fmt.Errorf("ollama: %w", context.Canceled)
	default:
		return fmt.Errorf("ollama: %w: %w", llm.ErrUnreachable, err)
	}
}

func statusError(status int, body []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &e) // a non-JSON body just leaves the message empty
	msg := e.Error
	if r := []rune(msg); len(r) > maxErrorBytes {
		msg = string(r[:maxErrorBytes])
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("ollama: %w: %s", llm.ErrModelNotFound, msg)
	}
	return fmt.Errorf("ollama: server returned status %d: %s", status, msg)
}

func decode(raw []byte) (llm.Explanation, error) {
	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return llm.Explanation{}, fmt.Errorf("ollama: %w: decode response: %w", llm.ErrMalformedResponse, err)
	}
	if resp.Message == nil {
		return llm.Explanation{}, fmt.Errorf("ollama: %w: response has no message", llm.ErrMalformedResponse)
	}
	if resp.DoneReason == "length" {
		return llm.Explanation{}, fmt.Errorf("ollama: %w: answer cut at %d tokens", llm.ErrMalformedResponse, maxPredict)
	}

	dec := json.NewDecoder(bytes.NewReader([]byte(resp.Message.Content)))
	dec.DisallowUnknownFields()
	var e llm.Explanation
	if err := dec.Decode(&e); err != nil {
		return llm.Explanation{}, fmt.Errorf("ollama: %w: decode answer: %w", llm.ErrMalformedResponse, err)
	}
	if dec.More() {
		return llm.Explanation{}, fmt.Errorf("ollama: %w: trailing data after answer", llm.ErrMalformedResponse)
	}
	if err := llm.Validate(e); err != nil {
		return llm.Explanation{}, fmt.Errorf("ollama: %w", err)
	}
	return e, nil
}
