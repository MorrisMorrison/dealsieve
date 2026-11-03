// Package notify sends deal notifications to ntfy and Telegram.
package notify

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/MorrisMorrison/dealsieve/internal/config"
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

// FromConfig builds one notifier per configured channel. The config is
// already validated, so unknown types don't occur.
func FromConfig(channels map[string]config.Channel, env config.Env, hc *http.Client) map[string]Notifier {
	out := make(map[string]Notifier, len(channels))
	for name, ch := range channels {
		switch ch.Type {
		case "ntfy":
			out[name] = &Ntfy{BaseURL: ch.URL, Topic: ch.Topic, Priority: ch.Priority, Token: env.NtfyToken, HTTP: hc}
		case "telegram":
			out[name] = &Telegram{APIBase: "https://api.telegram.org", Token: env.TelegramToken, ChatID: ch.ChatID, HTTP: hc}
		}
	}
	return out
}
