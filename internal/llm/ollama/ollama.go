// Package ollama implements llm.Explainer, and the server checks used by
// whyfail doctor, over the Ollama REST API.
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

// Client calls the Ollama REST API.
type Client struct {
	base  *url.URL
	model string
	http  *http.Client
}

// New returns a Client for the server at base using model. Request deadlines
// come from the context passed to each method.
func New(base *url.URL, model string, hc *http.Client) *Client {
	return &Client{base: base, model: model, http: hc}
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

	status, raw, err := c.do(ctx, http.MethodPost, "chat", body)
	if err != nil {
		return llm.Explanation{}, err
	}
	if status != http.StatusOK {
		return llm.Explanation{}, statusError(status, raw)
	}
	return decode(raw)
}

// Version returns the server version reported by /api/version.
func (c *Client) Version(ctx context.Context) (string, error) {
	status, raw, err := c.do(ctx, http.MethodGet, "version", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", statusError(status, raw)
	}
	var v struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("ollama: %w: decode version: %w", llm.ErrMalformedResponse, err)
	}
	if v.Version == "" {
		return "", fmt.Errorf("ollama: %w: empty version", llm.ErrMalformedResponse)
	}
	return v.Version, nil
}

// HasModel reports whether the configured model is installed. /api/show
// resolves tags and aliases the same way /api/chat does.
func (c *Client) HasModel(ctx context.Context) (bool, error) {
	body, err := json.Marshal(map[string]string{"model": c.model})
	if err != nil {
		return false, fmt.Errorf("ollama: encode request: %w", err)
	}
	status, raw, err := c.do(ctx, http.MethodPost, "show", body)
	switch {
	case err != nil:
		return false, err
	case status == http.StatusOK:
		return true, nil
	case status == http.StatusNotFound:
		return false, nil
	default:
		return false, statusError(status, raw)
	}
}

// do sends a request to /api/<path> and returns the status and the body,
// capped at maxResponseBytes.
func (c *Client) do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath("api", path).String(), r)
	if err != nil {
		return 0, nil, fmt.Errorf("ollama: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, transportError(ctx, err)
	}
	// The body is fully read below; a close error cannot change the outcome.
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, transportError(ctx, err)
	}
	if len(raw) > maxResponseBytes {
		return 0, nil, fmt.Errorf("ollama: %w: response larger than %d bytes", llm.ErrMalformedResponse, maxResponseBytes)
	}
	return resp.StatusCode, raw, nil
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
