// Package digest builds and schedules the daily summary notification: the
// watchlist hits and the top passed deals that are new since the previous
// digest, once a day per channel.
package digest

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
)

// MaxBytes keeps a digest inside one notification: Telegram allows 4096
// characters, ntfy 4096 bytes.
const MaxBytes = 4000

const maxTitle = 80

type Item struct {
	Title       string
	URL         string
	Price       string
	Temperature *int
}

// Due reports whether a channel's digest should go out at now: the local
// send time has passed today and the channel's last window ended before it.
func Due(now time.Time, d settings.Digest, last time.Time, hasLast bool) bool {
	h, m, ok := settings.ParseClock(d.Time)
	if !ok {
		return false
	}
	loc := d.Location()
	local := now.In(loc)
	// time.Date moves a time the clock skips (spring forward) to after the gap.
	sendAt := time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc)
	if local.Before(sendAt) {
		return false
	}
	return !hasLast || last.Before(sendAt)
}

// Compose builds one digest message. Lines from the end of the top list are
// dropped (and counted in "… and K more") until both versions fit MaxBytes.
func Compose(hits, top []Item, publicURL string) notify.Message {
	total := len(hits) + len(top)
	title := fmt.Sprintf("dealsieve · %d new deal", total)
	if total != 1 {
		title += "s"
	}
	shown, more := top, 0
	for {
		body, htm := render(title, hits, shown, more, publicURL)
		if (len(body) <= MaxBytes && len(htm) <= MaxBytes) || len(shown) == 0 {
			m := notify.Message{Title: title, Body: body, HTML: htm, ClickURL: publicURL}
			return m
		}
		shown, more = shown[:len(shown)-1], more+1
	}
}

func render(title string, hits, top []Item, more int, publicURL string) (plain, htm string) {
	var p, h []string
	if len(hits) > 0 {
		head := fmt.Sprintf("★ Watchlist (%d)", len(hits))
		p = append(p, head)
		h = append(h, "<b>"+html.EscapeString(head)+"</b>")
		for _, it := range hits {
			p = append(p, line("", it, false))
			h = append(h, line("", it, true))
		}
	}
	if len(top) > 0 || more > 0 {
		if len(p) > 0 {
			p, h = append(p, ""), append(h, "")
		}
		p = append(p, "Top deals")
		h = append(h, "<b>Top deals</b>")
		for i, it := range top {
			n := fmt.Sprintf("%d. ", i+1)
			p = append(p, line(n, it, false))
			h = append(h, line(n, it, true))
		}
		if more > 0 {
			tail := fmt.Sprintf("… and %d more", more)
			p, h = append(p, tail), append(h, html.EscapeString(tail))
		}
	}
	htmlLines := append([]string{"<b>" + html.EscapeString(title) + "</b>", ""}, h...)
	if publicURL != "" {
		htmlLines = append(htmlLines, "", fmt.Sprintf(`<a href="%s">Open dealsieve</a>`, html.EscapeString(publicURL)))
	}
	return strings.Join(p, "\n"), strings.Join(htmlLines, "\n")
}

func line(prefix string, it Item, asHTML bool) string {
	title := cut(it.Title)
	if asHTML {
		title = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(it.URL), html.EscapeString(title))
	}
	parts := []string{prefix + title}
	if it.Price != "" {
		parts = append(parts, maybeEscape(it.Price, asHTML))
	}
	if it.Temperature != nil {
		parts = append(parts, fmt.Sprintf("%d°", *it.Temperature))
	}
	return strings.Join(parts, " · ")
}

func maybeEscape(s string, asHTML bool) string {
	if asHTML {
		return html.EscapeString(s)
	}
	return s
}

func cut(s string) string {
	r := []rune(s)
	if len(r) <= maxTitle {
		return s
	}
	return string(r[:maxTitle-1]) + "…"
}
