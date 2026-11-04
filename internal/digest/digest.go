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
	// Source is the site label, empty when only one source is on.
	Source      string
	Title       string
	URL         string
	Price       string
	Temperature *int
	// Opinion is the judge's "<rating>: <reason>", or empty when there is none.
	Opinion string
}

// Due reports whether a channel's digest should go out at now: the local
// send time has passed today and the channel's last window did not end on
// today's local date. Comparing dates, not the send time, keeps a digest that
// already went out from being sent again after the time or zone is changed.
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
	return !hasLast || last.In(loc).Format(time.DateOnly) != local.Format(time.DateOnly)
}

// Compose builds one digest message. Each deal's judge opinion, if any, goes on
// its own line under the deal. Lines from the end of the top list are dropped
// (and counted in "… and K more") until both versions fit MaxBytes; once the
// top list is empty, watchlist hits are trimmed from the end the same way.
func Compose(hits, top []Item, publicURL string) notify.Message {
	total := len(hits) + len(top)
	title := fmt.Sprintf("dealsieve · %d new deal", total)
	if total != 1 {
		title += "s"
	}
	shownHits, shown, more := hits, top, 0
	for {
		body, htm := render(title, shownHits, shown, more, publicURL)
		if (len(body) <= MaxBytes && len(htm) <= MaxBytes) || len(shownHits)+len(shown) == 0 {
			return notify.Message{Title: title, Body: body, HTML: htm, ClickURL: publicURL}
		}
		if len(shown) > 0 {
			shown = shown[:len(shown)-1]
		} else {
			shownHits = shownHits[:len(shownHits)-1]
		}
		more++
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
	if len(top) > 0 {
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
	}
	if more > 0 {
		tail := fmt.Sprintf("… and %d more", more)
		if len(top) == 0 && len(p) > 0 {
			p, h = append(p, ""), append(h, "")
		}
		p, h = append(p, tail), append(h, html.EscapeString(tail))
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
	if it.Source != "" {
		title = maybeEscape(it.Source, asHTML) + " · " + title
	}
	parts := []string{prefix + title}
	if it.Price != "" {
		parts = append(parts, maybeEscape(it.Price, asHTML))
	}
	if it.Temperature != nil {
		parts = append(parts, fmt.Sprintf("%d°", *it.Temperature))
	}
	out := strings.Join(parts, " · ")
	if it.Opinion != "" {
		out += "\n   ↳ " + maybeEscape(it.Opinion, asHTML)
	}
	return out
}

func maybeEscape(s string, asHTML bool) string {
	if asHTML {
		return html.EscapeString(s)
	}
	return s
}

// cut flattens any whitespace run (line breaks, tabs) to one space, so a deal
// stays on one line, then shortens the title.
func cut(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= maxTitle {
		return s
	}
	return string(r[:maxTitle-1]) + "…"
}
