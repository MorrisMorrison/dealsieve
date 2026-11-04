package digest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
)

var berlin, _ = time.LoadLocation("Europe/Berlin")

func at(day, hhmm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hhmm, berlin)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDue(t *testing.T) {
	d := settings.Digest{Enabled: true, Time: "18:00", Timezone: "Europe/Berlin", Top: 10}
	tests := []struct {
		name    string
		now     time.Time
		last    time.Time
		hasLast bool
		want    bool
	}{
		{"before send time, never sent", at("2026-11-04", "17:59"), time.Time{}, false, false},
		{"at send time, never sent", at("2026-11-04", "18:00"), time.Time{}, false, true},
		{"after send time, sent yesterday", at("2026-11-04", "18:01"), at("2026-11-03", "18:00"), true, true},
		{"already sent today", at("2026-11-04", "21:00"), at("2026-11-04", "18:00"), true, false},
		{"catch-up later the same day", at("2026-11-04", "23:30"), at("2026-11-03", "18:00"), true, true},
		{"before send time, sent yesterday", at("2026-11-04", "09:00"), at("2026-11-03", "18:00"), true, false},
		{"missed a day", at("2026-11-05", "18:00"), at("2026-11-03", "18:00"), true, true},
		{"already caught up after today's send time", at("2026-11-04", "19:00"), at("2026-11-04", "18:30"), true, false},
		// Europe/Berlin switches to summer time on 2026-03-29 and back on 2026-10-25.
		{"spring forward day", at("2026-03-29", "18:00"), at("2026-03-28", "18:00"), true, true},
		{"fall back day", at("2026-10-25", "18:00"), at("2026-10-24", "18:00"), true, true},
		{"fall back day, one hour early", at("2026-10-25", "17:00"), at("2026-10-24", "18:00"), true, false},
	}
	for _, tc := range tests {
		if got := Due(tc.now.UTC(), d, tc.last.UTC(), tc.hasLast); got != tc.want {
			t.Errorf("%s: Due = %v, want %v", tc.name, got, tc.want)
		}
	}
	night := settings.Digest{Time: "02:30", Timezone: "Europe/Berlin"} // 02:30 doesn't exist on 2026-03-29
	if !Due(at("2026-03-29", "04:00").UTC(), night, at("2026-03-28", "02:30").UTC(), true) {
		t.Error("a send time skipped by the clock change must still fire that day")
	}
}

func temp(v int) *int { return &v }

func TestCompose(t *testing.T) {
	hits := []Item{{Title: "Example OLED TV 55\"", URL: "https://d.example.test/1", Price: "899€", Temperature: temp(312)}}
	top := []Item{
		{Title: "Ryzen 7 7800X3D", URL: "https://d.example.test/2", Price: "299€", Temperature: temp(637)},
		{Title: "Cables & <Adapters>", URL: "https://d.example.test/3?a=1&b=2"},
	}
	m := Compose(hits, top, "https://deals.example.com")
	if m.Title != "dealsieve · 3 new deals" {
		t.Errorf("title = %q", m.Title)
	}
	wantBody := "★ Watchlist (1)\nExample OLED TV 55\" · 899€ · 312°\n\nTop deals\n1. Ryzen 7 7800X3D · 299€ · 637°\n2. Cables & <Adapters>"
	if m.Body != wantBody {
		t.Errorf("body =\n%s\nwant\n%s", m.Body, wantBody)
	}
	if m.ClickURL != "https://deals.example.com" {
		t.Errorf("click = %q", m.ClickURL)
	}
	for _, want := range []string{
		"<b>dealsieve · 3 new deals</b>",
		`<a href="https://d.example.test/3?a=1&amp;b=2">Cables &amp; &lt;Adapters&gt;</a>`,
		"1. <a href=\"https://d.example.test/2\">Ryzen 7 7800X3D</a> · 299€ · 637°",
		`<a href="https://deals.example.com">Open dealsieve</a>`,
	} {
		if !strings.Contains(m.HTML, want) {
			t.Errorf("HTML lacks %q:\n%s", want, m.HTML)
		}
	}
}

func TestComposeSectionsAndSingular(t *testing.T) {
	m := Compose(nil, []Item{{Title: "One", URL: "https://d.example.test/1"}}, "")
	if m.Title != "dealsieve · 1 new deal" || strings.Contains(m.Body, "Watchlist") || m.ClickURL != "" || strings.Contains(m.HTML, "Open dealsieve") {
		t.Errorf("message = %+v", m)
	}
	m = Compose([]Item{{Title: "Hit", URL: "https://d.example.test/1"}}, nil, "")
	if strings.Contains(m.Body, "Top deals") {
		t.Errorf("empty top section shown: %q", m.Body)
	}
}

func TestComposeShowsOpinion(t *testing.T) {
	top := []Item{{Title: "Example Air Flash Sale", URL: "https://www.mydealz.de/deals/x-1", Price: "19,99€", Opinion: "bad: fees <make> it €90"}}
	m := Compose(nil, top, "")
	if !strings.Contains(m.Body, "1. Example Air Flash Sale · 19,99€\n   ↳ bad: fees <make> it €90") {
		t.Errorf("plain:\n%s", m.Body)
	}
	if !strings.Contains(m.HTML, "\n   ↳ bad: fees &lt;make&gt; it €90") {
		t.Errorf("html:\n%s", m.HTML)
	}
}

func TestComposeCutsLongTitles(t *testing.T) {
	m := Compose(nil, []Item{{Title: strings.Repeat("x", 100), URL: "https://d.example.test/1"}}, "")
	if !strings.Contains(m.Body, "1. "+strings.Repeat("x", 79)+"…") {
		t.Errorf("body = %q", m.Body)
	}
}

func TestComposeStaysUnderTheLimit(t *testing.T) {
	var top []Item
	for i := range 25 {
		top = append(top, Item{Title: strings.Repeat("é", 80), URL: fmt.Sprintf("https://d.example.test/%d?%s", i, strings.Repeat("q", 100)), Price: "999,99€", Temperature: temp(1000 + i)})
	}
	m := Compose(nil, top, "https://deals.example.com")
	if len(m.Body) > MaxBytes || len(m.HTML) > MaxBytes {
		t.Fatalf("body %d bytes, HTML %d bytes", len(m.Body), len(m.HTML))
	}
	if !strings.Contains(m.Body, "more") || !strings.HasPrefix(m.Title, "dealsieve · 25 new deals") {
		t.Errorf("title %q, body tail %q", m.Title, m.Body[len(m.Body)-40:])
	}
}

func TestComposeNamesTheSource(t *testing.T) {
	top := []Item{{Title: "Example", URL: "https://d.example.test/1", Price: "9€", Source: "dealabs"}}
	m := Compose(nil, top, "")
	if want := "1. dealabs · Example · 9€"; !strings.Contains(m.Body, want) {
		t.Errorf("body lacks %q:\n%s", want, m.Body)
	}
	if want := `1. dealabs · <a href="https://d.example.test/1">Example</a> · 9€`; !strings.Contains(m.HTML, want) {
		t.Errorf("HTML lacks %q:\n%s", want, m.HTML)
	}
}

func TestComposeSizeCapCountsTheSourceLabel(t *testing.T) {
	var top []Item
	for i := 0; i < 60; i++ {
		top = append(top, Item{Title: strings.Repeat("x", 80), URL: "https://d.example.test/1", Source: "preisjaeger"})
	}
	m := Compose(nil, top, "")
	if len(m.Body) > MaxBytes || len(m.HTML) > MaxBytes {
		t.Errorf("sizes body=%d html=%d, max %d", len(m.Body), len(m.HTML), MaxBytes)
	}
}
