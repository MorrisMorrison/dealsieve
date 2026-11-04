package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var ntfyPriority = map[string]int{"min": 1, "low": 2, "": 3, "default": 3, "high": 4, "urgent": 5}

type Ntfy struct {
	BaseURL  string
	Topic    string
	Priority string
	Token    string
	HTTP     *http.Client
}

// Send uses ntfy's JSON publishing, which keeps non-ASCII titles intact.
func (n *Ntfy) Send(ctx context.Context, m Message) error {
	payload := struct {
		Topic    string `json:"topic"`
		Title    string `json:"title,omitempty"`
		Message  string `json:"message,omitempty"`
		Priority int    `json:"priority"`
		Click    string `json:"click,omitempty"`
		Attach   string `json:"attach,omitempty"`
	}{n.Topic, m.Title, m.Body, ntfyPriority[n.Priority], m.ClickURL, m.ImageURL}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.BaseURL, "/")+"/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &statusError{what: "ntfy", code: resp.StatusCode, body: string(bytes.TrimSpace(msg))}
	}
	return nil
}
