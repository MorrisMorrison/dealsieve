// Package notify sends deal notifications to ntfy and Telegram.
package notify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
)

type Message struct {
	Title    string
	Body     string
	ClickURL string
	ImageURL string
}

type Notifier interface {
	Send(ctx context.Context, m Message) error
}

// statusError is a non-2xx answer from a target. Its full text includes the
// response body (useful in logs); short() leaves the body out.
type statusError struct {
	what string
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("%s: status %d: %s", e.what, e.code, e.body) }
func (e *statusError) short() string { return fmt.Sprintf("%s: status %d", e.what, e.code) }

// Reason describes a send error without anything a target answered, so it is
// safe to show to whoever can reach the settings page.
func Reason(err error) string {
	var se *statusError
	if errors.As(err, &se) {
		return se.short()
	}
	return err.Error()
}

func DealMessage(title, price, merchant string, temperature *int, reasons []string, url, imageURL string) Message {
	var parts []string
	if price != "" {
		parts = append(parts, price)
	}
	if temperature != nil {
		parts = append(parts, fmt.Sprintf("%d°", *temperature))
	}
	if merchant != "" {
		parts = append(parts, merchant)
	}
	body := strings.Join(parts, " · ")
	if len(reasons) > 0 {
		// One reason per line: reasons can contain commas themselves.
		body += "\n" + strings.Join(reasons, "\n")
	}
	return Message{Title: title, Body: body, ClickURL: url, ImageURL: imageURL}
}

// FromSettings builds one notifier per channel. Settings are validated, so
// unknown types don't occur; they are skipped all the same.
func FromSettings(channels map[string]settings.Channel, env config.Env, hc *http.Client) map[string]Notifier {
	out := make(map[string]Notifier, len(channels))
	for name, ch := range channels {
		if n := ForChannel(ch, env, hc); n != nil {
			out[name] = n
		}
	}
	return out
}

// ForChannel builds the notifier for one channel, e.g. to send a test
// message from unsaved form values. It returns nil for an unknown type.
func ForChannel(ch settings.Channel, env config.Env, hc *http.Client) Notifier {
	switch ch.Type {
	case "ntfy":
		return &Ntfy{BaseURL: ch.URL, Topic: ch.Topic, Priority: ch.Priority, Token: env.NtfyToken, HTTP: hc}
	case "telegram":
		return &Telegram{APIBase: "https://api.telegram.org", Token: env.TelegramToken, ChatID: ch.ChatID, HTTP: hc}
	}
	return nil
}
