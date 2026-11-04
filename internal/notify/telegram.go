package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Telegram struct {
	APIBase string
	Token   string
	ChatID  string
	HTTP    *http.Client
}

func (t *Telegram) Send(ctx context.Context, m Message) error {
	if m.HTML != "" {
		return t.call(ctx, "sendMessage", map[string]string{"chat_id": t.ChatID, "text": m.HTML, "parse_mode": "HTML"})
	}
	text := m.Title + "\n" + m.Body
	if m.ClickURL != "" {
		text += "\n" + m.ClickURL
	}
	if m.ImageURL != "" {
		err := t.call(ctx, "sendPhoto", map[string]string{"chat_id": t.ChatID, "photo": m.ImageURL, "caption": truncate(text, 1024)})
		if err == nil {
			return nil
		}
		// Only a 400 means Telegram couldn't use the photo; the text alone
		// still helps. Any other failure may have been delivered, so a
		// fallback could duplicate the message.
		var se *statusError
		if !errors.As(err, &se) || se.code != http.StatusBadRequest {
			return err
		}
	}
	return t.call(ctx, "sendMessage", map[string]string{"chat_id": t.ChatID, "text": truncate(text, 4096)})
}

func (t *Telegram) call(ctx context.Context, method string, payload map[string]string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := strings.TrimRight(t.APIBase, "/") + "/bot" + t.Token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: bad request", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		// The request URL contains the bot token; keep it out of logs.
		return fmt.Errorf("telegram %s: %s", method, strings.ReplaceAll(err.Error(), t.Token, "***"))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &statusError{what: "telegram " + method, code: resp.StatusCode, body: string(bytes.TrimSpace(msg))}
	}
	return nil
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
