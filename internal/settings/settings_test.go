package settings

import (
	"encoding/json"
	"maps"
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

func TestDigestDefaultsAndOldDocuments(t *testing.T) {
	d := Defaults().Digest
	if d.Enabled || d.Time != "18:00" || d.Timezone != "Europe/Berlin" || d.Top != 10 || d.Channels == nil || len(d.Channels) != 0 {
		t.Errorf("digest defaults = %+v", d)
	}
	// Documents saved before the digest existed have no "digest" key.
	s, err := Decode([]byte(`{"watchlist":["oled"],"rules":{"min_temperature":50,"category_overrides":{},"muted_categories":[],"muted_merchants":[],"block_keywords":[],"recurrence":{"window_days":90,"max_seen":2}},"channels":{},"notify":[],"suggestions":{"min_count":3,"window_days":30}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Digest.Time != "18:00" || s.Rules.MinTemperature != 50 || !slices.Equal(s.Watchlist, []string{"oled"}) {
		t.Errorf("decoded = %+v", s)
	}
	if errs := s.Normalize().Validate(); len(errs) != 0 {
		t.Errorf("old document invalid: %v", errs)
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string][2]int{"18:00": {18, 0}, "07:05": {7, 5}, "00:00": {0, 0}, "23:59": {23, 59}} {
		if h, m, ok := ParseClock(in); !ok || h != want[0] || m != want[1] {
			t.Errorf("ParseClock(%q) = %d, %d, %v", in, h, m, ok)
		}
	}
	for _, in := range []string{"", "7:05", "24:00", "12:60", "12-00", "12:00:00", "ab:cd"} {
		if _, _, ok := ParseClock(in); ok {
			t.Errorf("ParseClock(%q) ok", in)
		}
	}
}

func TestValidateDigest(t *testing.T) {
	tests := map[string]struct {
		edit func(*Digest)
		key  string
	}{
		"bad time":        {func(d *Digest) { d.Time = "25:00" }, "digest.time"},
		"unknown zone":    {func(d *Digest) { d.Timezone = "Mars/Olympus" }, "digest.timezone"},
		"empty zone":      {func(d *Digest) { d.Timezone = "" }, "digest.timezone"},
		"local zone":      {func(d *Digest) { d.Timezone = "Local" }, "digest.timezone"},
		"top too small":   {func(d *Digest) { d.Top = 0 }, "digest.top"},
		"top too big":     {func(d *Digest) { d.Top = 26 }, "digest.top"},
		"unknown channel": {func(d *Digest) { d.Channels = []string{"phone", "nope"} }, "digest.channels[1]"},
		"on, no channels": {func(d *Digest) { d.Enabled = true; d.Channels = []string{} }, "digest.channels"},
	}
	for name, tc := range tests {
		s := valid()
		tc.edit(&s.Digest)
		if errs := s.Validate(); errs[tc.key] == "" {
			t.Errorf("%s: errors %v, want %s", name, errs, tc.key)
		}
	}
	s := valid()
	s.Digest = Digest{Enabled: true, Time: "07:30", Timezone: "America/New_York", Top: 25, Channels: []string{"tg"}}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("valid digest: %v", errs)
	}
	if loc := s.Digest.Location(); loc.String() != "America/New_York" {
		t.Errorf("location = %v", loc)
	}
	if loc := (Digest{Timezone: "Mars/Olympus"}).Location(); loc != time.UTC {
		t.Errorf("bad zone location = %v", loc)
	}
}

func TestNormalizeDigest(t *testing.T) {
	s := valid()
	s.Digest = Digest{Time: " 18:00 ", Timezone: " Europe/Berlin ", Top: 10, Channels: []string{" phone ", "phone", ""}}
	n := s.Normalize().Digest
	if n.Time != "18:00" || n.Timezone != "Europe/Berlin" || !slices.Equal(n.Channels, []string{"phone"}) {
		t.Errorf("normalized = %+v", n)
	}
}

func TestJudgeDefaultsAndValidate(t *testing.T) {
	d := Defaults()
	if d.Judge != (Judge{DailyLimit: 200}) {
		t.Fatalf("defaults = %+v", d.Judge)
	}
	if errs := d.Validate(); len(errs) != 0 {
		t.Fatalf("defaults invalid: %v", errs)
	}
	tests := []struct {
		name  string
		judge Judge
		key   string
	}{
		{"enabled needs a model", Judge{Enabled: true, DailyLimit: 200}, "judge.model"},
		{"model without spaces", Judge{Model: "a model", DailyLimit: 200}, "judge.model"},
		{"model length", Judge{Model: strings.Repeat("m", 201), DailyLimit: 200}, "judge.model"},
		{"limit too low", Judge{DailyLimit: 0}, "judge.daily_limit"},
		{"limit too high", Judge{DailyLimit: 5001}, "judge.daily_limit"},
		{"instructions length", Judge{DailyLimit: 1, Instructions: strings.Repeat("i", 501)}, "judge.instructions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Defaults()
			s.Judge = tt.judge
			if _, ok := s.Normalize().Validate()[tt.key]; !ok {
				t.Errorf("want an error on %s, got %v", tt.key, s.Validate())
			}
		})
	}
	ok := Defaults()
	ok.Judge = Judge{Enabled: true, Model: " example/model ", DailyLimit: 50, Instructions: " Answer in German. "}
	n := ok.Normalize()
	if n.Judge.Model != "example/model" || n.Judge.Instructions != "Answer in German." {
		t.Errorf("normalize = %+v", n.Judge)
	}
	if errs := n.Validate(); len(errs) != 0 {
		t.Errorf("valid judge rejected: %v", errs)
	}
	if errs := ValidateJudge(Judge{DailyLimit: 1}, true); errs["judge.model"] == "" {
		t.Error("ValidateJudge(requireModel) accepted an empty model")
	}
}

func TestDecodeOldDocumentGetsJudgeDefaults(t *testing.T) {
	s, err := Decode([]byte(`{"watchlist":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Judge.DailyLimit != 200 || s.Judge.Enabled {
		t.Fatalf("judge = %+v", s.Judge)
	}
}

func TestSourcesDefaultsAndOldDocuments(t *testing.T) {
	d := Defaults()
	if got := d.EnabledSources(); !slices.Equal(got, []string{"mydealz"}) {
		t.Errorf("default enabled = %v", got)
	}
	if len(d.Sources) != len(SourceNames) {
		t.Errorf("defaults list %d sources, want %d", len(d.Sources), len(SourceNames))
	}
	// A document saved before sources existed.
	old, err := Decode([]byte(`{"watchlist": ["tv"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := old.EnabledSources(); !slices.Equal(got, []string{"mydealz"}) {
		t.Errorf("old document enabled = %v", got)
	}
	// A stored "off" must not be merged back to the default "on".
	off, err := Decode([]byte(`{"sources": {"mydealz": {"enabled": false}, "dealabs": {"enabled": true, "min_temperature": 80}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := off.EnabledSources(); !slices.Equal(got, []string{"dealabs"}) {
		t.Errorf("enabled = %v, want [dealabs]", got)
	}
	if got := off.SourceMinimums(); !maps.Equal(got, map[string]int{"dealabs": 80}) {
		t.Errorf("minimums = %v", got)
	}
}

func TestSourcesValidation(t *testing.T) {
	neg := -1
	cases := []struct {
		name    string
		sources map[string]Source
		key     string
	}{
		{"unknown", map[string]Source{"mydealz": {Enabled: true}, "Example": {Enabled: true}}, "sources.example"},
		{"none on", map[string]Source{"mydealz": {}}, "sources"},
		{"negative", map[string]Source{"mydealz": {Enabled: true, MinTemperature: &neg}}, "sources.mydealz.min_temperature"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Defaults()
			s.Sources = c.sources
			errs := s.Normalize().Validate()
			if _, ok := errs[c.key]; !ok {
				t.Errorf("errors %v, want key %q", errs, c.key)
			}
		})
	}
}

func TestNormalizeFillsSources(t *testing.T) {
	s := Defaults()
	s.Sources = map[string]Source{" HotUKDeals ": {Enabled: true}}
	n := s.Normalize()
	if len(n.Sources) != len(SourceNames) || !n.Sources["hotukdeals"].Enabled || n.Sources["mydealz"].Enabled {
		t.Errorf("normalized sources = %+v", n.Sources)
	}
}

func TestSourceLabel(t *testing.T) {
	if SourceLabel("pepperpl") != "pepper.pl" || SourceLabel("dealabs") != "dealabs" {
		t.Error("labels")
	}
}
