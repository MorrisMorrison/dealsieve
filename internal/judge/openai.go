package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// maxBody caps how much of a reply is read; an opinion is a few hundred bytes.
const maxBody = 1 << 20

// OpenAI talks to any OpenAI-compatible chat-completions API (OpenRouter,
// Ollama, llama.cpp, vLLM, ...).
type OpenAI struct {
	BaseURL      string // without the trailing /chat/completions
	APIKey       string // optional; local servers often need none
	Model        string
	Instructions string
	UserAgent    string
	Referer      string // OpenRouter's optional app attribution
	HTTP         *http.Client
}

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP %d", e.Status) }

// Retryable means the server is busy or rate-limiting, so the caller should
// stop for now rather than try the next deal.
func (e *HTTPError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

var errNoReply = errors.New("no reply")

func (o *OpenAI) Judge(ctx context.Context, d Deal) (Opinion, error) {
	body, err := json.Marshal(map[string]any{
		"model":       o.Model,
		"messages":    Prompt(d, o.Instructions),
		"max_tokens":  400,
		"temperature": 0,
	})
	if err != nil {
		return Opinion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Opinion{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.UserAgent != "" {
		req.Header.Set("User-Agent", o.UserAgent)
	}
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	if o.Referer != "" {
		req.Header.Set("HTTP-Referer", o.Referer)
	}
	req.Header.Set("X-Title", "dealsieve")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return Opinion{}, fmt.Errorf("judge request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return Opinion{}, &HTTPError{Status: resp.StatusCode}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return Opinion{}, fmt.Errorf("%w: %v", ErrBadReply, err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return Opinion{}, errNoReply
	}
	return Parse(out.Choices[0].Message.Content)
}

// Retryable reports whether err means "back off and try later".
func Retryable(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Retryable()
	}
	return isTimeout(err)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// Reason is a short description of err that is safe to log and show: it
// never contains a response body or the key.
func Reason(err error) string {
	var he *HTTPError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &he):
		return he.Error()
	case errors.Is(err, ErrDisabled):
		return ErrDisabled.Error()
	case errors.Is(err, ErrBadReply):
		return ErrBadReply.Error()
	case errors.Is(err, errNoReply):
		return errNoReply.Error()
	case isTimeout(err):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "couldn't reach the server"
	}
}
