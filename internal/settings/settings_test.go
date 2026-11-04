package settings

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func valid() Settings {
	s := Defaults()
	s.Watchlist = []string{"oled"}
	s.Channels = map[string]Channel{
		"phone": {Type: "ntfy", URL: "https://ntfy.example.com", Topic: "deals", Priority: "high"},
		"tg":    {Type: "telegram", ChatID: "123"},
	}
	s.Notify = []NotifyRule{
		{When: When{Watchlist: true}, Channels: []string{"phone", "tg"}},
		{When: When{MinTemperature: 500}, Channels: []string{"phone"}},
	}
	return s
}

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Rules.MinTemperature != 100 || d.Rules.Recurrence != (Recurrence{WindowDays: 90, MaxSeen: 2}) ||
		d.Suggestions != (Suggestions{MinCount: 3, WindowDays: 30}) {
		t.Errorf("defaults = %+v", d)
	}
	if d.Watchlist == nil || d.Channels == nil || d.Notify == nil || d.Rules.CategoryOverrides == nil {
		t.Error("defaults must use empty, not nil, lists and maps (they marshal as [] and {})")
	}
	if errs := d.Validate(); len(errs) != 0 {
		t.Errorf("defaults invalid: %v", errs)
	}
	if d.Rules.Recurrence.Window() != 90*24*time.Hour {
		t.Errorf("window = %v", d.Rules.Recurrence.Window())
	}
}

func TestJSONRoundTrip(t *testing.T) {
	in := valid()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"when":"watchlist"`) || !strings.Contains(string(raw), `"when":{"min_temperature":500}`) {
		t.Errorf("when encoding = %s", raw)
	}
	out, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Notify[1].When.MinTemperature != 500 || !out.Notify[0].When.Watchlist || out.Channels["tg"].ChatID != "123" {
		t.Errorf("round trip = %+v", out)
	}
}

func TestDecodeRejectsUnknownKeysAndBadConditions(t *testing.T) {
	for _, in := range []string{
		`{"watchlist":[],"nope":1}`,
		`{"rules":{"min_temp":5}}`,
		`{"notify":[{"when":"always","channels":["x"]}]}`,
		`{"notify":[{"when":{"max_temperature":5},"channels":["x"]}]}`,
		`{"watchlist":[]} {"x":1}`,
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%s) = nil error", in)
		}
	}
}

func TestNormalize(t *testing.T) {
	s := Defaults()
	s.Watchlist = []string{" OLED ", "oled", "", "Lego"}
	s.Rules.MutedMerchants = []string{"Example Air", "example air "}
	s.Rules.CategoryOverrides = map[string]int{" Elektronik ": 60}
	s.Channels = map[string]Channel{" phone ": {Type: " NTFY ", URL: " https://ntfy.example.com ", Topic: " deals ", Priority: " High "}}
	s.Watchlist, s.Notify = append(s.Watchlist, "lego"), nil
	n := s.Normalize()
	if !slices.Equal(n.Watchlist, []string{"OLED", "Lego"}) {
		t.Errorf("watchlist = %q", n.Watchlist)
	}
	if !slices.Equal(n.Rules.MutedMerchants, []string{"Example Air"}) {
		t.Errorf("muted = %q", n.Rules.MutedMerchants)
	}
	if n.Rules.CategoryOverrides["Elektronik"] != 60 {
		t.Errorf("overrides = %v", n.Rules.CategoryOverrides)
	}
	ch, ok := n.Channels["phone"]
	if !ok || ch.Type != "ntfy" || ch.URL != "https://ntfy.example.com" || ch.Topic != "deals" || ch.Priority != "high" {
		t.Errorf("channel = %+v (found %v)", ch, ok)
	}
	if n.Notify == nil {
		t.Error("nil notify not replaced by an empty list")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Settings)
		path   string
	}{
		{"empty override category", func(s *Settings) { s.Rules.CategoryOverrides[""] = 5 }, "rules.category_overrides"},
		{"override categories differing in case", func(s *Settings) {
			s.Rules.CategoryOverrides["Reisen"] = 5
			s.Rules.CategoryOverrides["reisen"] = 7
		}, "rules.category_overrides"},
		{"window zero", func(s *Settings) { s.Rules.Recurrence.WindowDays = 0 }, "rules.recurrence.window_days"},
		{"window above retention", func(s *Settings) { s.Rules.Recurrence.WindowDays = 181 }, "rules.recurrence.window_days"},
		{"max seen zero", func(s *Settings) { s.Rules.Recurrence.MaxSeen = 0 }, "rules.recurrence.max_seen"},
		{"bad channel name", func(s *Settings) { s.Channels["My Phone"] = Channel{Type: "telegram", ChatID: "1"} }, "channels.My Phone"},
		{"unknown channel type", func(s *Settings) { s.Channels["x"] = Channel{Type: "email"} }, "channels.x.type"},
		{"ntfy bad url", func(s *Settings) { s.Channels["x"] = Channel{Type: "ntfy", URL: "ftp://x", Topic: "t"} }, "channels.x.url"},
		{"ntfy url without host", func(s *Settings) { s.Channels["x"] = Channel{Type: "ntfy", URL: "http://:80", Topic: "t"} }, "channels.x.url"},
		{"ntfy no topic", func(s *Settings) { s.Channels["x"] = Channel{Type: "ntfy", URL: "https://n.example.com"} }, "channels.x.topic"},
		{"ntfy bad priority", func(s *Settings) {
			s.Channels["x"] = Channel{Type: "ntfy", URL: "https://n.example.com", Topic: "t", Priority: "loud"}
		}, "channels.x.priority"},
		{"telegram no chat", func(s *Settings) { s.Channels["x"] = Channel{Type: "telegram"} }, "channels.x.chat_id"},
		{"notify without condition", func(s *Settings) { s.Notify[1].When = When{} }, "notify[1].when"},
		{"notify without channels", func(s *Settings) { s.Notify[0].Channels = nil }, "notify[0].channels"},
		{"notify unknown channel", func(s *Settings) { s.Notify[0].Channels = []string{"phone", "tv"} }, "notify[0].channels[1]"},
		{"suggestions min count", func(s *Settings) { s.Suggestions.MinCount = 1 }, "suggestions.min_count"},
		{"suggestions window", func(s *Settings) { s.Suggestions.WindowDays = 0 }, "suggestions.window_days"},
	}
	if errs := valid().Validate(); len(errs) != 0 {
		t.Fatalf("valid settings rejected: %v", errs)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := valid()
			tt.change(&s)
			if errs := s.Validate(); errs[tt.path] == "" {
				t.Errorf("errors = %v, want one at %q", errs, tt.path)
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	s := valid()
	if w := Warnings(s, true); len(w) != 0 {
		t.Errorf("with token: %v", w)
	}
	w := Warnings(s, false)
	if len(w) != 1 || !strings.Contains(w["tg"], "DEALSIEVE_TELEGRAM_TOKEN") {
		t.Errorf("without token: %v", w)
	}
}
