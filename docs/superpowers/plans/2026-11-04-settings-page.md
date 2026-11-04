# Settings Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `config.yaml` with an in-app Settings page (watchlist, filters, recurrence, notification channels and rules), stored as versioned JSON documents in SQLite with history and undo, applied without a restart, plus rule suggestions from the user's Hide/Junk clicks.

**Architecture:** A new pure `settings` package owns the document type, defaults, normalisation, validation and change summaries. The store gains versioned settings rows, dismissed suggestions and a "clicked deals" query; a pure `suggest` package turns those clicks into suggestions. A `settingsvc` service loads/saves/undoes settings and applies them to the pipeline (which swaps its rule engine and notifiers under its mutex). `config` shrinks to environment variables. The API exposes settings, history, test sends, suggestions and categories; a new `settings.html` page edits them.

**Tech Stack:** Go 1.26 (stdlib `net/http`, `encoding/json`), `modernc.org/sqlite`, vanilla HTML/CSS/ES modules, Node 24 `node --test`.

**Spec:** `docs/superpowers/specs/2026-11-04-settings-page-design.md`. Issue #21. Branch `feat/settings-page`.

## Global Constraints

- Go dependencies: only `modernc.org/sqlite` remains. `gopkg.in/yaml.v3` is removed (Task 5). No new dependencies.
- Frontend: plain HTML, CSS and ES modules; no framework, no npm packages in `web/`, no build step. Build the DOM with `textContent` and attributes, never `innerHTML` with data.
- `CGO_ENABLED=0` must build.
- The repo is public: no personal infrastructure, domains, hosts or local paths anywhere; examples use `example.com` / `example.test`.
- Secrets (`DEALSIEVE_TELEGRAM_TOKEN`, `DEALSIEVE_NTFY_TOKEN`) never enter the settings document, the database or an API response.
- `MaxWindowDays = 180` (deal retention); recurrence and suggestion windows must be 1–180.
- Defaults: `min_temperature` 100, recurrence 90 days / max_seen 2, suggestions min_count 3 / window_days 30, everything else empty.
- Poll env vars: `DEALSIEVE_POLL_TRENDING` (default `5m`), `DEALSIEVE_POLL_NEW` (default `2m`), minimum `1m`. `DEALSIEVE_CONFIG` and `config.yaml` are no longer read.
- Mute shop / mute category from a deal or a suggestion **modify the settings lists**; Hide and Junk remain actions.
- Recompute after a settings change never notifies (existing behaviour) and uses `context.WithoutCancel` so a dropped request can't stop it halfway.
- Every commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`; commit bodies `Refs #21`. Code is gofmt-clean; comments sparse and explain *why*.
- Make sure `go` (1.26+) and `node` (24+) are on PATH. `-race` needs cgo; where no C compiler is available run `go test` without it (CI runs it). On checkouts with `core.autocrlf=true`, `gofmt -l .` also lists untouched files because of CRLF working copies: check only the Go files you changed.

## File Structure

```
internal/settings/settings.go        Settings types, Defaults, Decode, Normalize, Validate, ValidateChannel, Warnings
internal/settings/summary.go         Summarize(prev, next) []string
internal/store/migrations/002_settings.sql
internal/store/settings.go           settings versions, dismissals, clicked deals, categories, DeleteActionsByValue
internal/suggest/suggest.go          Compute(clicks, settings, dismissed) []Suggestion
internal/settingsvc/service.go       Service: Load, Current, Save, Undo, Mute, History, Suggestions, ApplySuggestion, Dismiss
internal/config/config.go            Env only (FromEnv); duration.go deleted
internal/api/settings.go             settings, history, test-send, suggestions, categories handlers
web/settings.html, web/static/css/settings.css
web/static/js/settings.js            settings page controller
web/static/js/settings-model.js      pure helpers (+ settings-model.test.js)
```

---

### Task 1: The `settings` package

**Files:**
- Create: `internal/settings/settings.go`, `internal/settings/summary.go`
- Test: `internal/settings/settings_test.go`, `internal/settings/summary_test.go`

**Interfaces:**
- Produces:
  - `type Settings struct { Watchlist []string; Rules Rules; Channels map[string]Channel; Notify []NotifyRule; Suggestions Suggestions }` (JSON keys `watchlist`, `rules`, `channels`, `notify`, `suggestions`)
  - `type Rules struct { MinTemperature int; CategoryOverrides map[string]int; MutedCategories, MutedMerchants, BlockKeywords []string; Recurrence Recurrence }` (JSON `min_temperature`, `category_overrides`, `muted_categories`, `muted_merchants`, `block_keywords`, `recurrence`)
  - `type Recurrence struct { WindowDays, MaxSeen int }` (`window_days`, `max_seen`); `func (r Recurrence) Window() time.Duration`
  - `type Channel struct { Type, URL, Topic, Priority, ChatID string }` (`type`, `url`, `topic`, `priority`, `chat_id`; the last four `omitempty`)
  - `type NotifyRule struct { When When; Channels []string }`; `type When struct { Watchlist bool; MinTemperature int }` (JSON: `"watchlist"` or `{"min_temperature": N}`)
  - `type Suggestions struct { MinCount, WindowDays int }` (`min_count`, `window_days`)
  - `const MaxWindowDays = 180`
  - `func Defaults() Settings`, `func Decode(data []byte) (Settings, error)`, `func (s Settings) Normalize() Settings`, `func (s Settings) Validate() map[string]string`, `func ValidateChannel(name string, ch Channel) map[string]string`, `func Warnings(s Settings, telegramToken bool) map[string]string`, `func Summarize(prev, next Settings) []string`

- [ ] **Step 1: Write the failing tests**

`internal/settings/settings_test.go`:
```go
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
		{"window zero", func(s *Settings) { s.Rules.Recurrence.WindowDays = 0 }, "rules.recurrence.window_days"},
		{"window above retention", func(s *Settings) { s.Rules.Recurrence.WindowDays = 181 }, "rules.recurrence.window_days"},
		{"max seen zero", func(s *Settings) { s.Rules.Recurrence.MaxSeen = 0 }, "rules.recurrence.max_seen"},
		{"bad channel name", func(s *Settings) { s.Channels["My Phone"] = Channel{Type: "telegram", ChatID: "1"} }, "channels.My Phone"},
		{"unknown channel type", func(s *Settings) { s.Channels["x"] = Channel{Type: "email"} }, "channels.x.type"},
		{"ntfy bad url", func(s *Settings) { s.Channels["x"] = Channel{Type: "ntfy", URL: "ftp://x", Topic: "t"} }, "channels.x.url"},
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
```

`internal/settings/summary_test.go`:
```go
package settings

import (
	"slices"
	"testing"
)

func TestSummarize(t *testing.T) {
	prev := valid()
	next := valid()
	next.Watchlist = []string{"oled", "lego"}
	next.Rules.MutedMerchants = []string{"Example Air"}
	next.Rules.MinTemperature = 80
	next.Rules.CategoryOverrides = map[string]int{"Elektronik": 60}
	next.Rules.Recurrence = Recurrence{WindowDays: 60, MaxSeen: 3}
	delete(next.Channels, "tg")
	next.Notify = []NotifyRule{{When: When{Watchlist: true}, Channels: []string{"phone"}}}
	next.Suggestions = Suggestions{MinCount: 4, WindowDays: 30}

	want := []string{
		`Added watchlist entry "lego"`,
		`Added muted shop "Example Air"`,
		`Minimum temperature 100° → 80°`,
		`Added minimum for "Elektronik" (60°)`,
		`Recurring deals: 2× in 90 days → 3× in 60 days`,
		`Removed channel "tg"`,
		`Changed notify rules`,
		`Suggestions: 3 in 30 days → 4 in 30 days`,
	}
	if got := Summarize(prev, next); !slices.Equal(got, want) {
		t.Errorf("Summarize =\n%q\nwant\n%q", got, want)
	}
}

func TestSummarizeDetails(t *testing.T) {
	a := valid()
	b := valid()
	if got := Summarize(a, b); len(got) != 0 {
		t.Errorf("identical settings: %q", got)
	}
	b.Watchlist = []string{"OLED"} // case-only change is not a change
	if got := Summarize(a, b); len(got) != 0 {
		t.Errorf("case-only change: %q", got)
	}
	a.Rules.CategoryOverrides = map[string]int{"Elektronik": 60, "Gaming": 80}
	b.Rules.CategoryOverrides = map[string]int{"Elektronik": 50}
	b.Rules.BlockKeywords = []string{"gratisprobe"}
	a.Rules.MutedCategories = []string{"Reisen"}
	b.Channels["phone"] = Channel{Type: "ntfy", URL: "https://ntfy.example.com", Topic: "other", Priority: "high"}
	want := []string{
		`Removed muted category "Reisen"`,
		`Added block keyword "gratisprobe"`,
		`Minimum for "Elektronik" 60° → 50°`,
		`Removed minimum for "Gaming"`,
		`Changed channel "phone"`,
	}
	if got := Summarize(a, b); !slices.Equal(got, want) {
		t.Errorf("Summarize =\n%q\nwant\n%q", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/settings/`
Expected: FAIL (build errors: `undefined: Defaults`, …).

- [ ] **Step 3: Implement settings.go**

`internal/settings/settings.go`:
```go
// Package settings is the user-editable configuration: watchlist, filters,
// recurrence, notifications and suggestion thresholds, stored as one JSON
// document. Infrastructure and secrets stay in environment variables.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MaxWindowDays matches how long deals are kept: a longer window would
// silently under-count.
const MaxWindowDays = 180

type Settings struct {
	Watchlist   []string           `json:"watchlist"`
	Rules       Rules              `json:"rules"`
	Channels    map[string]Channel `json:"channels"`
	Notify      []NotifyRule       `json:"notify"`
	Suggestions Suggestions        `json:"suggestions"`
}

type Rules struct {
	MinTemperature    int            `json:"min_temperature"`
	CategoryOverrides map[string]int `json:"category_overrides"`
	MutedCategories   []string       `json:"muted_categories"`
	MutedMerchants    []string       `json:"muted_merchants"`
	BlockKeywords     []string       `json:"block_keywords"`
	Recurrence        Recurrence     `json:"recurrence"`
}

type Recurrence struct {
	WindowDays int `json:"window_days"`
	MaxSeen    int `json:"max_seen"`
}

func (r Recurrence) Window() time.Duration { return time.Duration(r.WindowDays) * 24 * time.Hour }

type Channel struct {
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	Topic    string `json:"topic,omitempty"`
	Priority string `json:"priority,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
}

type NotifyRule struct {
	When     When     `json:"when"`
	Channels []string `json:"channels"`
}

// When is a notify condition: "watchlist" or {"min_temperature": N}.
type When struct {
	Watchlist      bool
	MinTemperature int
}

func (w When) MarshalJSON() ([]byte, error) {
	if w.Watchlist {
		return json.Marshal("watchlist")
	}
	return json.Marshal(struct {
		MinTemperature int `json:"min_temperature"`
	}{w.MinTemperature})
}

func (w *When) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if s != "watchlist" {
			return fmt.Errorf("unknown condition %q", s)
		}
		*w = When{Watchlist: true}
		return nil
	}
	var m struct {
		MinTemperature int `json:"min_temperature"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf(`condition must be "watchlist" or {"min_temperature": N}: %w`, err)
	}
	*w = When{MinTemperature: m.MinTemperature}
	return nil
}

type Suggestions struct {
	MinCount   int `json:"min_count"`
	WindowDays int `json:"window_days"`
}

func Defaults() Settings {
	return Settings{
		Watchlist: []string{},
		Rules: Rules{
			MinTemperature:    100,
			CategoryOverrides: map[string]int{},
			MutedCategories:   []string{},
			MutedMerchants:    []string{},
			BlockKeywords:     []string{},
			Recurrence:        Recurrence{WindowDays: 90, MaxSeen: 2},
		},
		Channels:    map[string]Channel{},
		Notify:      []NotifyRule{},
		Suggestions: Suggestions{MinCount: 3, WindowDays: 30},
	}
}

// Decode parses a settings document strictly: unknown keys are errors.
func Decode(data []byte) (Settings, error) {
	var s Settings
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Settings{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Settings{}, errors.New("unexpected data after the settings object")
	}
	return s, nil
}

// Normalize trims values, lowercases channel types and priorities, drops
// empty and case-insensitively duplicated list entries, and replaces nil
// lists and maps with empty ones.
func (s Settings) Normalize() Settings {
	out := s
	out.Watchlist = cleanList(s.Watchlist)
	out.Rules.MutedCategories = cleanList(s.Rules.MutedCategories)
	out.Rules.MutedMerchants = cleanList(s.Rules.MutedMerchants)
	out.Rules.BlockKeywords = cleanList(s.Rules.BlockKeywords)
	out.Rules.CategoryOverrides = make(map[string]int, len(s.Rules.CategoryOverrides))
	for k, v := range s.Rules.CategoryOverrides {
		out.Rules.CategoryOverrides[strings.TrimSpace(k)] = v
	}
	out.Channels = make(map[string]Channel, len(s.Channels))
	for name, ch := range s.Channels {
		out.Channels[strings.TrimSpace(name)] = Channel{
			Type:     strings.ToLower(strings.TrimSpace(ch.Type)),
			URL:      strings.TrimSpace(ch.URL),
			Topic:    strings.TrimSpace(ch.Topic),
			Priority: strings.ToLower(strings.TrimSpace(ch.Priority)),
			ChatID:   strings.TrimSpace(ch.ChatID),
		}
	}
	out.Notify = make([]NotifyRule, 0, len(s.Notify))
	for _, n := range s.Notify {
		out.Notify = append(out.Notify, NotifyRule{When: n.When, Channels: cleanList(n.Channels)})
	}
	return out
}

func cleanList(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		key := strings.ToLower(v)
		if v == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

var (
	channelName    = regexp.MustCompile(`^[a-z0-9-]+$`)
	ntfyPriorities = map[string]bool{"": true, "min": true, "low": true, "default": true, "high": true, "urgent": true}
)

// Validate returns problems keyed by field path; an empty map means valid.
// Call it on normalised settings.
func (s Settings) Validate() map[string]string {
	errs := map[string]string{}
	for cat := range s.Rules.CategoryOverrides {
		if strings.TrimSpace(cat) == "" {
			errs["rules.category_overrides"] = "category names can't be empty"
		}
	}
	if w := s.Rules.Recurrence.WindowDays; w < 1 || w > MaxWindowDays {
		errs["rules.recurrence.window_days"] = fmt.Sprintf("must be between 1 and %d days", MaxWindowDays)
	}
	if s.Rules.Recurrence.MaxSeen < 1 {
		errs["rules.recurrence.max_seen"] = "must be at least 1"
	}
	for name, ch := range s.Channels {
		for k, v := range ValidateChannel(name, ch) {
			errs[k] = v
		}
	}
	for i, n := range s.Notify {
		p := fmt.Sprintf("notify[%d]", i)
		if !n.When.Watchlist && n.When.MinTemperature <= 0 {
			errs[p+".when"] = "must be a watchlist match or a temperature above 0°"
		}
		if len(n.Channels) == 0 {
			errs[p+".channels"] = "needs at least one channel"
		}
		for j, c := range n.Channels {
			if _, ok := s.Channels[c]; !ok {
				errs[fmt.Sprintf("%s.channels[%d]", p, j)] = fmt.Sprintf("unknown channel %q", c)
			}
		}
	}
	if s.Suggestions.MinCount < 2 {
		errs["suggestions.min_count"] = "must be at least 2"
	}
	if w := s.Suggestions.WindowDays; w < 1 || w > MaxWindowDays {
		errs["suggestions.window_days"] = fmt.Sprintf("must be between 1 and %d days", MaxWindowDays)
	}
	return errs
}

// ValidateChannel checks one channel; the test-send endpoint uses it on
// unsaved form values.
func ValidateChannel(name string, ch Channel) map[string]string {
	errs := map[string]string{}
	p := "channels." + name
	if !channelName.MatchString(name) {
		errs[p] = "names use only a-z, 0-9 and -"
	}
	switch ch.Type {
	case "ntfy":
		if u, err := url.Parse(ch.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs[p+".url"] = "must be an http(s) URL"
		}
		if ch.Topic == "" {
			errs[p+".topic"] = "is required"
		}
		if !ntfyPriorities[ch.Priority] {
			errs[p+".priority"] = "must be min, low, default, high or urgent"
		}
	case "telegram":
		if ch.ChatID == "" {
			errs[p+".chat_id"] = "is required"
		}
	default:
		errs[p+".type"] = `must be "ntfy" or "telegram"`
	}
	return errs
}

// Warnings lists channels that are valid but can't send, keyed by channel
// name: a Telegram channel needs the bot token from the environment.
func Warnings(s Settings, telegramToken bool) map[string]string {
	w := map[string]string{}
	if telegramToken {
		return w
	}
	for name, ch := range s.Channels {
		if ch.Type == "telegram" {
			w[name] = "DEALSIEVE_TELEGRAM_TOKEN isn't set on the server, so this channel can't send."
		}
	}
	return w
}
```

- [ ] **Step 4: Implement summary.go**

`internal/settings/summary.go`:
```go
package settings

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Summarize describes what changed from prev to next in short sentences,
// for the settings history. No sentences means nothing changed.
func Summarize(prev, next Settings) []string {
	var out []string
	out = append(out, listDiff("watchlist entry", prev.Watchlist, next.Watchlist)...)
	out = append(out, listDiff("muted shop", prev.Rules.MutedMerchants, next.Rules.MutedMerchants)...)
	out = append(out, listDiff("muted category", prev.Rules.MutedCategories, next.Rules.MutedCategories)...)
	out = append(out, listDiff("block keyword", prev.Rules.BlockKeywords, next.Rules.BlockKeywords)...)
	if p, n := prev.Rules.MinTemperature, next.Rules.MinTemperature; p != n {
		out = append(out, fmt.Sprintf("Minimum temperature %d° → %d°", p, n))
	}
	out = append(out, overrideDiff(prev.Rules.CategoryOverrides, next.Rules.CategoryOverrides)...)
	if p, n := prev.Rules.Recurrence, next.Rules.Recurrence; p != n {
		out = append(out, fmt.Sprintf("Recurring deals: %d× in %d days → %d× in %d days", p.MaxSeen, p.WindowDays, n.MaxSeen, n.WindowDays))
	}
	out = append(out, channelDiff(prev.Channels, next.Channels)...)
	if !notifyEqual(prev.Notify, next.Notify) {
		out = append(out, "Changed notify rules")
	}
	if p, n := prev.Suggestions, next.Suggestions; p != n {
		out = append(out, fmt.Sprintf("Suggestions: %d in %d days → %d in %d days", p.MinCount, p.WindowDays, n.MinCount, n.WindowDays))
	}
	return out
}

func listDiff(label string, prev, next []string) []string {
	has := func(list []string, v string) bool {
		return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, v) })
	}
	var out []string
	for _, v := range next {
		if !has(prev, v) {
			out = append(out, fmt.Sprintf("Added %s %q", label, v))
		}
	}
	for _, v := range prev {
		if !has(next, v) {
			out = append(out, fmt.Sprintf("Removed %s %q", label, v))
		}
	}
	return out
}

func overrideDiff(prev, next map[string]int) []string {
	var out []string
	for _, k := range sortedKeys(prev, next) {
		p, inPrev := prev[k]
		n, inNext := next[k]
		switch {
		case !inPrev:
			out = append(out, fmt.Sprintf("Added minimum for %q (%d°)", k, n))
		case !inNext:
			out = append(out, fmt.Sprintf("Removed minimum for %q", k))
		case p != n:
			out = append(out, fmt.Sprintf("Minimum for %q %d° → %d°", k, p, n))
		}
	}
	return out
}

func channelDiff(prev, next map[string]Channel) []string {
	var out []string
	for _, k := range sortedKeys(prev, next) {
		p, inPrev := prev[k]
		n, inNext := next[k]
		switch {
		case !inPrev:
			out = append(out, fmt.Sprintf("Added channel %q", k))
		case !inNext:
			out = append(out, fmt.Sprintf("Removed channel %q", k))
		case p != n:
			out = append(out, fmt.Sprintf("Changed channel %q", k))
		}
	}
	return out
}

func notifyEqual(a, b []NotifyRule) bool {
	return slices.EqualFunc(a, b, func(x, y NotifyRule) bool {
		return x.When == y.When && slices.Equal(x.Channels, y.Channels)
	})
}

func sortedKeys[V any](a, b map[string]V) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []map[string]V{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/settings/`
Expected: `ok`. (If `TestNormalize` disagrees on order, the expectation is: first occurrence wins and keeps its trimmed original case.)

- [ ] **Step 6: Commit**

```bash
git add internal/settings
git commit -m "feat(settings): settings document with defaults, validation and change summaries" -m "Refs #21" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Store: settings versions, dismissals, clicked deals, categories

**Files:**
- Create: `internal/store/migrations/002_settings.sql`, `internal/store/settings.go`
- Test: `internal/store/settings_test.go`

**Interfaces:**
- Consumes: existing store helpers `scanner`, `fromUnix`, `ErrNotFound`, `ActionHide`, `ActionJunk`, `ActionMuteCategory`, `ActionMuteMerchant`, `UpsertDeal`, `AddAction`.
- Produces:
  - `type SettingsVersion struct { ID int64; Document []byte; Summary []string; CreatedAt time.Time }`
  - `func (s *Store) SaveSettings(ctx context.Context, document []byte, summary []string, now time.Time) (SettingsVersion, error)`
  - `func (s *Store) LatestSettings(ctx context.Context) (SettingsVersion, error)` (`ErrNotFound` when none)
  - `func (s *Store) SettingsBefore(ctx context.Context, id int64) (SettingsVersion, error)` (newest with ID < id; `ErrNotFound`)
  - `func (s *Store) SettingsHistory(ctx context.Context, limit int) ([]SettingsVersion, error)` (newest first)
  - `func (s *Store) DismissSuggestion(ctx context.Context, kind, value string, now time.Time) error` (stores `value` lowercased; repeating is a no-op)
  - `func (s *Store) DismissedSuggestions(ctx context.Context) (map[string]bool, error)` (keys `kind + "|" + lowercase value`)
  - `type ClickedDeal struct { DealID, Title, Merchant, Category string; ClickedAt time.Time }`
  - `func (s *Store) ClickedDealsSince(ctx context.Context, since time.Time) ([]ClickedDeal, error)` (Hide → the hidden deal; Junk → the most recently seen deal with that fingerprint; oldest click first)
  - `func (s *Store) Categories(ctx context.Context) ([]string, error)` (distinct, non-empty, sorted)
  - `func (s *Store) DeleteActionsByValue(ctx context.Context, kind, value string) (int64, error)`

- [ ] **Step 1: Write the failing tests**

`internal/store/settings_test.go`:
```go
package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func TestSettingsVersions(t *testing.T) {
	s := openTest(t)
	if _, err := s.LatestSettings(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty store: %v", err)
	}
	v1, err := s.SaveSettings(ctx, []byte(`{"a":1}`), []string{"Initial settings"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.SaveSettings(ctx, []byte(`{"a":2}`), []string{"Added x", "Removed y"}, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestSettings(ctx)
	if err != nil || latest.ID != v2.ID || string(latest.Document) != `{"a":2}` ||
		!slices.Equal(latest.Summary, []string{"Added x", "Removed y"}) || !latest.CreatedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("latest = %+v, %v", latest, err)
	}
	prev, err := s.SettingsBefore(ctx, v2.ID)
	if err != nil || prev.ID != v1.ID {
		t.Errorf("before v2 = %+v, %v", prev, err)
	}
	if _, err := s.SettingsBefore(ctx, v1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("before v1: %v", err)
	}
	hist, err := s.SettingsHistory(ctx, 1)
	if err != nil || len(hist) != 1 || hist[0].ID != v2.ID {
		t.Errorf("history = %+v, %v", hist, err)
	}
}

func TestDismissedSuggestions(t *testing.T) {
	s := openTest(t)
	for range 2 {
		if err := s.DismissSuggestion(ctx, ActionMuteMerchant, "Example Air", t0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DismissedSuggestions(ctx)
	if err != nil || len(got) != 1 || !got["mute_merchant|example air"] {
		t.Errorf("dismissed = %v, %v", got, err)
	}
}

func TestClickedDealsSince(t *testing.T) {
	s := openTest(t)
	mk := func(id, merchant, category, fp string, at time.Time) {
		d := deal(id, nil)
		d.Merchant, d.Category = merchant, category
		if _, err := s.UpsertDeal(ctx, d, source.FeedTrending, "x", fp, at); err != nil {
			t.Fatal(err)
		}
	}
	mk("1", "Example Air", "Reisen", "air|a", t0)
	mk("2", "Example Air", "Reisen", "air|b", t0)
	mk("3", "Example Air", "Reisen", "air|b", t0.Add(time.Hour)) // newer deal with the junked fingerprint
	mk("4", "Shop", "Elektronik", "shop|c", t0)
	add := func(kind, value string, at time.Time) {
		if _, err := s.AddAction(ctx, kind, value, at); err != nil {
			t.Fatal(err)
		}
	}
	add(ActionHide, "mydealz:1", t0.Add(-40*24*time.Hour)) // outside the window
	add(ActionHide, "mydealz:4", t0.Add(time.Minute))
	add(ActionJunk, "air|b", t0.Add(2*time.Minute))
	add(ActionHide, "mydealz:404", t0.Add(3*time.Minute)) // pruned deal: skipped

	got, err := s.ClickedDealsSince(ctx, t0.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.DealID)
	}
	if !slices.Equal(ids, []string{"mydealz:4", "mydealz:3"}) {
		t.Fatalf("clicked = %+v", got)
	}
	if got[1].Merchant != "Example Air" || got[1].Category != "Reisen" || !got[1].ClickedAt.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("junk row = %+v", got[1])
	}
}

func TestCategories(t *testing.T) {
	s := openTest(t)
	for i, c := range []string{"Reisen", "", "Elektronik", "Reisen"} {
		d := deal(string(rune('a'+i)), nil)
		d.Category = c
		if _, err := s.UpsertDeal(ctx, d, source.FeedTrending, "x", "fp", t0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Categories(ctx)
	if err != nil || !slices.Equal(got, []string{"Elektronik", "Reisen"}) {
		t.Errorf("categories = %q, %v", got, err)
	}
}

func TestDeleteActionsByValue(t *testing.T) {
	s := openTest(t)
	s.AddAction(ctx, ActionJunk, "fp", t0)
	s.AddAction(ctx, ActionJunk, "fp", t0)
	s.AddAction(ctx, ActionHide, "fp", t0)
	n, err := s.DeleteActionsByValue(ctx, ActionJunk, "fp")
	if err != nil || n != 2 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	if left, _ := s.Actions(ctx); len(left) != 1 || left[0].Kind != ActionHide {
		t.Errorf("left = %+v", left)
	}
}

func TestMigrationDropsMuteActions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddAction(ctx, ActionMuteMerchant, "Example Air", t0)
	s.AddAction(ctx, ActionHide, "mydealz:1", t0)
	// Pretend only migration 001 ran, so 002 runs again on the next open.
	if _, err := s.db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	left, _ := s.Actions(ctx)
	if len(left) != 1 || left[0].Kind != ActionHide {
		t.Errorf("actions after migration = %+v", left)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL (`s.LatestSettings undefined`, …).

- [ ] **Step 3: Write the migration**

`internal/store/migrations/002_settings.sql`:
```sql
CREATE TABLE IF NOT EXISTS settings_versions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    document   TEXT NOT NULL,
    summary    TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS dismissed_suggestions (
    kind       TEXT NOT NULL,
    value      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (kind, value)
);

-- Muting now lives in the settings lists.
DELETE FROM actions WHERE kind IN ('mute_category', 'mute_merchant');
```

- [ ] **Step 4: Implement settings.go**

`internal/store/settings.go`:
```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type SettingsVersion struct {
	ID        int64
	Document  []byte
	Summary   []string
	CreatedAt time.Time
}

const settingsColumns = `id, document, summary, created_at`

func scanSettings(sc scanner) (SettingsVersion, error) {
	var v SettingsVersion
	var doc, summary string
	var created int64
	if err := sc.Scan(&v.ID, &doc, &summary, &created); err != nil {
		return SettingsVersion{}, err
	}
	v.Document, v.CreatedAt = []byte(doc), fromUnix(created)
	return v, json.Unmarshal([]byte(summary), &v.Summary)
}

func (s *Store) SaveSettings(ctx context.Context, document []byte, summary []string, now time.Time) (SettingsVersion, error) {
	if summary == nil {
		summary = []string{}
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return SettingsVersion{}, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO settings_versions (document, summary, created_at) VALUES (?, ?, ?)`,
		string(document), string(raw), now.Unix())
	if err != nil {
		return SettingsVersion{}, err
	}
	id, err := res.LastInsertId()
	return SettingsVersion{ID: id, Document: document, Summary: summary, CreatedAt: fromUnix(now.Unix())}, err
}

func (s *Store) LatestSettings(ctx context.Context) (SettingsVersion, error) {
	return s.oneSettings(ctx, `SELECT `+settingsColumns+` FROM settings_versions ORDER BY id DESC LIMIT 1`)
}

// SettingsBefore returns the newest version older than id.
func (s *Store) SettingsBefore(ctx context.Context, id int64) (SettingsVersion, error) {
	return s.oneSettings(ctx, `SELECT `+settingsColumns+` FROM settings_versions WHERE id < ? ORDER BY id DESC LIMIT 1`, id)
}

func (s *Store) oneSettings(ctx context.Context, query string, args ...any) (SettingsVersion, error) {
	v, err := scanSettings(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return SettingsVersion{}, ErrNotFound
	}
	return v, err
}

// SettingsHistory returns up to limit versions, newest first.
func (s *Store) SettingsHistory(ctx context.Context, limit int) ([]SettingsVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+settingsColumns+` FROM settings_versions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SettingsVersion
	for rows.Next() {
		v, err := scanSettings(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DismissSuggestion(ctx context.Context, kind, value string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO dismissed_suggestions (kind, value, created_at) VALUES (?, ?, ?)`,
		kind, strings.ToLower(strings.TrimSpace(value)), now.Unix())
	return err
}

// DismissedSuggestions returns the dismissals keyed "kind|lowercase value".
func (s *Store) DismissedSuggestions(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, value FROM dismissed_suggestions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return nil, err
		}
		out[kind+"|"+value] = true
	}
	return out, rows.Err()
}

type ClickedDeal struct {
	DealID    string
	Title     string
	Merchant  string
	Category  string
	ClickedAt time.Time
}

// ClickedDealsSince returns the deals the user hid or marked as junk since
// since, oldest click first. A junk action stores a fingerprint, so it is
// represented by the most recently seen deal with that fingerprint. Clicks
// whose deal was pruned are left out.
func (s *Store) ClickedDealsSince(ctx context.Context, since time.Time) ([]ClickedDeal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.created_at AS clicked_at, a.id AS action_id, d.id, d.title, d.merchant, d.category
		FROM actions a JOIN deals d ON d.id = a.value
		WHERE a.kind = ? AND a.created_at >= ?
		UNION ALL
		SELECT a.created_at, a.id, d.id, d.title, d.merchant, d.category
		FROM actions a JOIN deals d ON d.id = (
			SELECT id FROM deals WHERE fingerprint = a.value ORDER BY last_seen DESC, id DESC LIMIT 1)
		WHERE a.kind = ? AND a.created_at >= ?
		ORDER BY clicked_at, action_id`,
		ActionHide, since.Unix(), ActionJunk, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClickedDeal
	for rows.Next() {
		var c ClickedDeal
		var at, actionID int64
		if err := rows.Scan(&at, &actionID, &c.DealID, &c.Title, &c.Merchant, &c.Category); err != nil {
			return nil, err
		}
		c.ClickedAt = fromUnix(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Categories(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT category FROM deals WHERE category <> '' ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteActionsByValue(ctx context.Context, kind, value string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM actions WHERE kind = ? AND value = ?`, kind, value)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/store/`
Expected: `ok` (all old and new store tests).

- [ ] **Step 6: Commit**

```bash
git add internal/store
git commit -m "feat(store): settings versions, dismissed suggestions, clicked deals and categories" -m "Refs #21" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The `suggest` package

**Files:**
- Create: `internal/suggest/suggest.go`
- Test: `internal/suggest/suggest_test.go`

**Interfaces:**
- Consumes: `settings.Settings` (Task 1), `store.ClickedDeal` (Task 2).
- Produces: `const KindMuteMerchant = "mute_merchant"`, `KindMuteCategory = "mute_category"`; `type Suggestion struct { Kind string; Value string; Count int; Since time.Time; Examples []string }` (JSON `kind`, `value`, `count`, `since`, `examples`); `func Compute(clicks []store.ClickedDeal, s settings.Settings, dismissed map[string]bool) []Suggestion` (never nil).

- [ ] **Step 1: Write the failing tests**

`internal/suggest/suggest_test.go`:
```go
package suggest

import (
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func click(id, title, merchant, category string, minutes int) store.ClickedDeal {
	return store.ClickedDeal{DealID: id, Title: title, Merchant: merchant, Category: category, ClickedAt: t0.Add(time.Duration(minutes) * time.Minute)}
}

func TestCompute(t *testing.T) {
	clicks := []store.ClickedDeal{
		click("1", "Flash Sale 19,99", "Example Air", "Reisen", 0),
		click("2", "Flash Sale 24,99", "example air", "Reisen", 1),
		click("2", "Flash Sale 24,99", "Example Air", "Reisen", 2), // same deal twice counts once
		click("3", "Flash Sale 17,99", "Example Air", "Reisen", 3),
		click("4", "Flash Sale 21,99", "Example Air", "Reisen", 4),
		click("5", "Kabel", "Shop", "Elektronik", 5),
		click("6", "Lampe", "", "Elektronik", 6),
	}
	got := Compute(clicks, settings.Defaults(), nil)
	if len(got) != 2 {
		t.Fatalf("suggestions = %+v", got)
	}
	m := got[0]
	if m.Kind != KindMuteMerchant || m.Value != "Example Air" || m.Count != 4 || !m.Since.Equal(t0) ||
		!slices.Equal(m.Examples, []string{"Flash Sale 21,99", "Flash Sale 17,99", "Flash Sale 24,99"}) {
		t.Errorf("merchant suggestion = %+v", m)
	}
	if c := got[1]; c.Kind != KindMuteCategory || c.Value != "Reisen" || c.Count != 4 {
		t.Errorf("category suggestion = %+v", c)
	}
}

func TestComputeSkipsMutedDismissedAndSmallGroups(t *testing.T) {
	clicks := []store.ClickedDeal{
		click("1", "a", "Example Air", "Reisen", 0),
		click("2", "b", "Example Air", "Reisen", 1),
		click("3", "c", "Example Air", "Reisen", 2),
		click("4", "d", "Shop", "Gaming", 3),
		click("5", "e", "Shop", "Gaming", 4),
	}
	s := settings.Defaults()
	s.Rules.MutedMerchants = []string{"EXAMPLE AIR"}
	if got := Compute(clicks, s, map[string]bool{"mute_category|reisen": true}); len(got) != 0 {
		t.Errorf("suggestions = %+v (muted, dismissed and below-threshold groups must be skipped)", got)
	}
	s.Suggestions.MinCount = 2
	got := Compute(clicks, s, map[string]bool{"mute_category|reisen": true})
	var keys []string
	for _, g := range got {
		keys = append(keys, g.Kind+":"+g.Value)
	}
	if !slices.Equal(keys, []string{"mute_merchant:Shop", "mute_category:Gaming"}) {
		t.Errorf("with min_count 2: %q", keys)
	}
}

func TestComputeEmpty(t *testing.T) {
	if got := Compute(nil, settings.Defaults(), nil); got == nil || len(got) != 0 {
		t.Errorf("got %#v, want an empty non-nil slice", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/suggest/`
Expected: FAIL (`undefined: Compute`).

- [ ] **Step 3: Implement**

`internal/suggest/suggest.go`:
```go
// Package suggest turns the user's Hide and Junk clicks into suggestions to
// mute a shop or a category.
package suggest

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

const (
	KindMuteMerchant = "mute_merchant"
	KindMuteCategory = "mute_category"
	maxExamples      = 3
)

type Suggestion struct {
	Kind     string    `json:"kind"`
	Value    string    `json:"value"`
	Count    int       `json:"count"`
	Since    time.Time `json:"since"`
	Examples []string  `json:"examples"`
}

type group struct {
	value         string
	deals         map[string]bool
	since, latest time.Time
	titles        []string // oldest first
}

// Compute groups clicks (oldest first) by lowercase shop and category and
// suggests muting groups that reached the threshold, unless they are
// already muted or were dismissed ("kind|lowercase value").
func Compute(clicks []store.ClickedDeal, s settings.Settings, dismissed map[string]bool) []Suggestion {
	groups := map[string]map[string]*group{KindMuteMerchant: {}, KindMuteCategory: {}}
	for _, c := range clicks {
		for kind, value := range map[string]string{KindMuteMerchant: c.Merchant, KindMuteCategory: c.Category} {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			key := strings.ToLower(value)
			g := groups[kind][key]
			if g == nil {
				g = &group{deals: map[string]bool{}, since: c.ClickedAt}
				groups[kind][key] = g
			}
			g.deals[c.DealID] = true
			if c.ClickedAt.Before(g.since) {
				g.since = c.ClickedAt
			}
			if !c.ClickedAt.Before(g.latest) {
				g.latest, g.value = c.ClickedAt, value
			}
			if !slices.Contains(g.titles, c.Title) {
				g.titles = append(g.titles, c.Title)
			}
		}
	}

	muted := map[string]map[string]bool{
		KindMuteMerchant: lowerSet(s.Rules.MutedMerchants),
		KindMuteCategory: lowerSet(s.Rules.MutedCategories),
	}
	type ranked struct {
		Suggestion
		latest time.Time
	}
	var all []ranked
	for kind, byKey := range groups {
		for key, g := range byKey {
			if len(g.deals) < s.Suggestions.MinCount || muted[kind][key] || dismissed[kind+"|"+key] {
				continue
			}
			examples := slices.Clone(g.titles)
			slices.Reverse(examples)
			if len(examples) > maxExamples {
				examples = examples[:maxExamples]
			}
			all = append(all, ranked{Suggestion{Kind: kind, Value: g.value, Count: len(g.deals), Since: g.since, Examples: examples}, g.latest})
		}
	}
	slices.SortFunc(all, func(a, b ranked) int {
		return cmp.Or(
			cmp.Compare(b.Count, a.Count),
			b.latest.Compare(a.latest),
			cmp.Compare(kindOrder(a.Kind), kindOrder(b.Kind)),
			cmp.Compare(a.Value, b.Value),
		)
	})
	out := make([]Suggestion, 0, len(all))
	for _, r := range all {
		out = append(out, r.Suggestion)
	}
	return out
}

func kindOrder(kind string) int {
	if kind == KindMuteMerchant {
		return 0
	}
	return 1
}

func lowerSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, v := range in {
		out[strings.ToLower(strings.TrimSpace(v))] = true
	}
	return out
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/suggest/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/suggest
git commit -m "feat(suggest): suggest muting shops and categories from repeated clicks" -m "Refs #21" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The `settingsvc` service

**Files:**
- Create: `internal/settingsvc/service.go`
- Test: `internal/settingsvc/service_test.go`

**Interfaces:**
- Consumes: Tasks 1–3 (`settings.*`, `store.*Settings*`, `store.DismissSuggestion`, `store.DismissedSuggestions`, `store.ClickedDealsSince`, `suggest.Compute`, `suggest.KindMute*`).
- Produces:
  - `type Applier interface { ApplySettings(settings.Settings); RecomputeRecent(ctx context.Context) (int, error) }`
  - `var ErrConflict, ErrNothingToUndo error`; `type ValidationError map[string]string` (implements `error`)
  - `type Snapshot struct { Version int64; UpdatedAt time.Time; Settings settings.Settings }`
  - `type Result struct { Snapshot; Rechecked int; Changed bool }`
  - `type HistoryEntry struct { Version int64; CreatedAt time.Time; Summary []string }` (JSON `version`, `created_at`, `summary`)
  - `type Service struct { Store *store.Store; Apply Applier; Now func() time.Time }` with methods
    `Load(ctx) error`, `Current() Snapshot`, `Save(ctx, version int64, next settings.Settings) (Result, error)`, `Undo(ctx) (Result, error)`, `Mute(ctx, kind, value, note string) (Result, error)`, `History(ctx, limit int) ([]HistoryEntry, error)`, `Suggestions(ctx) ([]suggest.Suggestion, error)`, `ApplySuggestion(ctx, kind, value string) (Result, error)`, `Dismiss(ctx, kind, value string) error`.

- [ ] **Step 1: Write the failing tests**

`internal/settingsvc/service_test.go`:
```go
package settingsvc

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
	"github.com/MorrisMorrison/dealsieve/internal/suggest"
)

var ctx = context.Background()

type fakeApplier struct {
	applied   []settings.Settings
	recompute int
}

func (f *fakeApplier) ApplySettings(s settings.Settings) { f.applied = append(f.applied, s) }
func (f *fakeApplier) RecomputeRecent(context.Context) (int, error) {
	f.recompute++
	return 42, nil
}

func newService(t *testing.T) (*Service, *fakeApplier, *store.Store, *time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	ap := &fakeApplier{}
	svc := &Service{Store: st, Apply: ap, Now: func() time.Time { return now }}
	if err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return svc, ap, st, &now
}

func TestLoadCreatesDefaultsAndApplies(t *testing.T) {
	svc, ap, st, _ := newService(t)
	cur := svc.Current()
	if cur.Version == 0 || cur.Settings.Rules.MinTemperature != 100 || len(ap.applied) != 1 {
		t.Fatalf("current = %+v, applied %d", cur, len(ap.applied))
	}
	v, err := st.LatestSettings(ctx)
	if err != nil || !slices.Equal(v.Summary, []string{"Initial settings"}) {
		t.Errorf("stored = %+v, %v", v, err)
	}
	// A second service on the same store loads the stored version instead of new defaults.
	again := &Service{Store: st, Apply: &fakeApplier{}, Now: svc.Now}
	if err := again.Load(ctx); err != nil || again.Current().Version != cur.Version {
		t.Errorf("reload = %+v, %v", again.Current(), err)
	}
}

func TestSave(t *testing.T) {
	svc, ap, _, _ := newService(t)
	v1 := svc.Current().Version
	next := svc.Current().Settings
	next.Watchlist = []string{" oled ", "OLED"}
	res, err := svc.Save(ctx, v1, next)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Rechecked != 42 || res.Version == v1 || !slices.Equal(res.Settings.Watchlist, []string{"oled"}) {
		t.Errorf("result = %+v", res)
	}
	if len(ap.applied) != 2 || ap.recompute != 1 {
		t.Errorf("applied %d, recomputed %d", len(ap.applied), ap.recompute)
	}
	if _, err := svc.Save(ctx, v1, next); !errors.Is(err, ErrConflict) {
		t.Errorf("stale version: %v", err)
	}
	same, err := svc.Save(ctx, res.Version, next)
	if err != nil || same.Changed || same.Version != res.Version || ap.recompute != 1 {
		t.Errorf("unchanged save = %+v, %v (no new version, no recompute)", same, err)
	}
	bad := svc.Current().Settings
	bad.Rules.Recurrence.WindowDays = 0
	var verr ValidationError
	if _, err := svc.Save(ctx, res.Version, bad); !errors.As(err, &verr) || verr["rules.recurrence.window_days"] == "" {
		t.Errorf("invalid save: %v", err)
	}
	hist, err := svc.History(ctx, 10)
	if err != nil || len(hist) != 2 || !slices.Equal(hist[0].Summary, []string{`Added watchlist entry "oled"`}) {
		t.Errorf("history = %+v, %v", hist, err)
	}
}

func TestUndo(t *testing.T) {
	svc, _, _, _ := newService(t)
	if _, err := svc.Undo(ctx); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo with one version: %v", err)
	}
	next := svc.Current().Settings
	next.Rules.MinTemperature = 80
	if _, err := svc.Save(ctx, svc.Current().Version, next); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Undo(ctx)
	if err != nil || res.Settings.Rules.MinTemperature != 100 || !res.Changed {
		t.Fatalf("undo = %+v, %v", res, err)
	}
	hist, _ := svc.History(ctx, 1)
	if !slices.Equal(hist[0].Summary, []string{"Undid: Minimum temperature 100° → 80°"}) {
		t.Errorf("undo summary = %q", hist[0].Summary)
	}
}

func TestMuteAndSuggestions(t *testing.T) {
	svc, _, st, now := newService(t)
	for i, id := range []string{"1", "2", "3"} {
		d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
			Title: "Flash Sale " + id, Merchant: "Example Air", Category: "Reisen"}
		if _, err := st.UpsertDeal(ctx, d, source.FeedTrending, "x", "fp"+id, *now); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddAction(ctx, store.ActionHide, d.ID, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.Suggestions(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("suggestions = %+v, %v", got, err)
	}
	res, err := svc.ApplySuggestion(ctx, suggest.KindMuteMerchant, "Example Air")
	if err != nil || !slices.Equal(res.Settings.Rules.MutedMerchants, []string{"Example Air"}) {
		t.Fatalf("apply = %+v, %v", res, err)
	}
	hist, _ := svc.History(ctx, 1)
	if !slices.Equal(hist[0].Summary, []string{`Added muted shop "Example Air" (suggested)`}) {
		t.Errorf("summary = %q", hist[0].Summary)
	}
	if err := svc.Dismiss(ctx, suggest.KindMuteCategory, "Reisen"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Suggestions(ctx); len(got) != 0 {
		t.Errorf("after apply + dismiss: %+v", got)
	}
	if _, err := svc.Mute(ctx, "mute_everything", "x", ""); err == nil {
		t.Error("unknown kind accepted")
	}
	if err := svc.Dismiss(ctx, "nope", "x"); err == nil {
		t.Error("unknown dismiss kind accepted")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/settingsvc/`
Expected: FAIL (`undefined: Service`).

- [ ] **Step 3: Implement**

`internal/settingsvc/service.go`:
```go
// Package settingsvc loads, validates, versions and applies the settings,
// and turns the user's clicks into rule suggestions.
package settingsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/store"
	"github.com/MorrisMorrison/dealsieve/internal/suggest"
)

// Applier puts settings into effect (the pipeline).
type Applier interface {
	ApplySettings(settings.Settings)
	RecomputeRecent(ctx context.Context) (int, error)
}

var (
	ErrConflict      = errors.New("the settings were changed in the meantime")
	ErrNothingToUndo = errors.New("there is no earlier version to go back to")
)

// ValidationError maps field paths to problems.
type ValidationError map[string]string

func (e ValidationError) Error() string { return fmt.Sprintf("%d invalid settings", len(e)) }

type Snapshot struct {
	Version   int64
	UpdatedAt time.Time
	Settings  settings.Settings
}

type Result struct {
	Snapshot
	Rechecked int
	Changed   bool
}

type HistoryEntry struct {
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Summary   []string  `json:"summary"`
}

// Service serialises all settings changes; the current settings are only
// read through Current.
type Service struct {
	Store *store.Store
	Apply Applier
	Now   func() time.Time

	mu  sync.Mutex
	cur Snapshot
}

// Load reads the newest stored settings (saving the defaults on first start)
// and applies them without re-checking deals.
func (s *Service) Load(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Store.LatestSettings(ctx)
	if errors.Is(err, store.ErrNotFound) {
		v, err = s.write(ctx, settings.Defaults(), []string{"Initial settings"})
	}
	if err != nil {
		return err
	}
	cur, err := settings.Decode(v.Document)
	if err != nil {
		return fmt.Errorf("stored settings version %d: %w", v.ID, err)
	}
	s.cur = Snapshot{Version: v.ID, UpdatedAt: v.CreatedAt, Settings: cur.Normalize()}
	s.Apply.ApplySettings(s.cur.Settings)
	return nil
}

// Current returns the settings in effect. Treat them as read-only.
func (s *Service) Current() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func (s *Service) Save(ctx context.Context, version int64, next settings.Settings) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version != s.cur.Version {
		return Result{}, ErrConflict
	}
	next = next.Normalize()
	if errs := next.Validate(); len(errs) > 0 {
		return Result{}, ValidationError(errs)
	}
	return s.commit(ctx, next, settings.Summarize(s.cur.Settings, next))
}

// Undo saves the version before the current one as a new version.
func (s *Service) Undo(ctx context.Context) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, err := s.Store.SettingsBefore(ctx, s.cur.Version)
	if errors.Is(err, store.ErrNotFound) {
		return Result{}, ErrNothingToUndo
	}
	if err != nil {
		return Result{}, err
	}
	latest, err := s.Store.LatestSettings(ctx)
	if err != nil {
		return Result{}, err
	}
	next, err := settings.Decode(prev.Document)
	if err != nil {
		return Result{}, fmt.Errorf("stored settings version %d: %w", prev.ID, err)
	}
	return s.commit(ctx, next.Normalize(), []string{"Undid: " + strings.Join(latest.Summary, "; ")})
}

// Mute adds a shop or category to the muted lists; note is appended to the
// history summary (e.g. " (from a deal)").
func (s *Service) Mute(ctx context.Context, kind, value, note string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := clone(s.cur.Settings)
	if err != nil {
		return Result{}, err
	}
	switch kind {
	case suggest.KindMuteMerchant:
		next.Rules.MutedMerchants = append(next.Rules.MutedMerchants, value)
	case suggest.KindMuteCategory:
		next.Rules.MutedCategories = append(next.Rules.MutedCategories, value)
	default:
		return Result{}, fmt.Errorf("unknown mute kind %q", kind)
	}
	next = next.Normalize()
	summary := settings.Summarize(s.cur.Settings, next)
	for i := range summary {
		summary[i] += note
	}
	return s.commit(ctx, next, summary)
}

func (s *Service) History(ctx context.Context, limit int) ([]HistoryEntry, error) {
	versions, err := s.Store.SettingsHistory(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(versions))
	for _, v := range versions {
		out = append(out, HistoryEntry{Version: v.ID, CreatedAt: v.CreatedAt, Summary: v.Summary})
	}
	return out, nil
}

func (s *Service) Suggestions(ctx context.Context) ([]suggest.Suggestion, error) {
	cur := s.Current().Settings
	since := s.Now().Add(-time.Duration(cur.Suggestions.WindowDays) * 24 * time.Hour)
	clicks, err := s.Store.ClickedDealsSince(ctx, since)
	if err != nil {
		return nil, err
	}
	dismissed, err := s.Store.DismissedSuggestions(ctx)
	if err != nil {
		return nil, err
	}
	return suggest.Compute(clicks, cur, dismissed), nil
}

func (s *Service) ApplySuggestion(ctx context.Context, kind, value string) (Result, error) {
	return s.Mute(ctx, kind, value, " (suggested)")
}

func (s *Service) Dismiss(ctx context.Context, kind, value string) error {
	if kind != suggest.KindMuteMerchant && kind != suggest.KindMuteCategory {
		return fmt.Errorf("unknown suggestion kind %q", kind)
	}
	return s.Store.DismissSuggestion(ctx, kind, value, s.Now())
}

// commit stores next as a new version (if anything changed), applies it and
// re-checks recent deals. Callers hold s.mu.
func (s *Service) commit(ctx context.Context, next settings.Settings, summary []string) (Result, error) {
	if len(summary) == 0 {
		return Result{Snapshot: s.cur}, nil
	}
	v, err := s.write(ctx, next, summary)
	if err != nil {
		return Result{}, err
	}
	s.cur = Snapshot{Version: v.ID, UpdatedAt: v.CreatedAt, Settings: next}
	s.Apply.ApplySettings(next)
	// The change is saved; finish re-checking even if the client goes away.
	n, err := s.Apply.RecomputeRecent(context.WithoutCancel(ctx))
	if err != nil {
		return Result{}, fmt.Errorf("re-check deals: %w", err)
	}
	return Result{Snapshot: s.cur, Rechecked: n, Changed: true}, nil
}

func (s *Service) write(ctx context.Context, next settings.Settings, summary []string) (store.SettingsVersion, error) {
	doc, err := json.Marshal(next)
	if err != nil {
		return store.SettingsVersion{}, err
	}
	return s.Store.SaveSettings(ctx, doc, summary, s.Now())
}

func clone(s settings.Settings) (settings.Settings, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return settings.Settings{}, err
	}
	return settings.Decode(raw)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/settingsvc/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/settingsvc
git commit -m "feat(settingsvc): load, save, undo and apply versioned settings; suggestions" -m "Refs #21" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Switch the app from config.yaml to settings

The biggest task: it rewires existing packages. After it, `config.yaml` is gone and the app runs on stored settings. Keep every existing test's intent; only change what the new types require.

**Files:**
- Modify: `internal/config/config.go`; delete `internal/config/duration.go`; rewrite `internal/config/config_test.go`
- Modify: `internal/rules/rules.go` (+ `rules_test.go`), `internal/notify/notify.go` (+ `notify_test.go`), `internal/pipeline/pipeline.go` (+ `pipeline_test.go`), `internal/api/api.go`; create `internal/api/settings.go`; modify `internal/api/api_test.go`; create `internal/api/settings_test.go`
- Modify: `cmd/dealsieve/main.go`, `cmd/dealsieve/main_test.go`, `test/e2e/e2e_test.go`
- Modify: `screenshots/capture.mjs`; delete `screenshots/config.yaml`; create `screenshots/settings.json`; modify `.github/workflows/screenshots.yml` only if it references `config.yaml`
- `go.mod` / `go.sum` (`go mod tidy` drops yaml.v3)

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces:
  - `config.Env{DataDir, Listen, LogFormat, TelegramToken, NtfyToken, MydealzURL string; PollTrending, PollNew time.Duration}`, `config.FromEnv(getenv func(string) string) (Env, error)`, `config.MinPollInterval`
  - `rules.New(r settings.Rules, watchlist []string) *Engine`
  - `notify.FromSettings(channels map[string]settings.Channel, env config.Env, hc *http.Client) map[string]Notifier`, `notify.ForChannel(ch settings.Channel, env config.Env, hc *http.Client) Notifier` (nil for unknown types)
  - `pipeline.Pipeline` gains `Env config.Env`, `HTTP *http.Client`, `Notify []settings.NotifyRule`, method `ApplySettings(settings.Settings)`; `RecomputeRecent(ctx) (int, error)`
  - `api.Server{Store, Recompute, Settings *settingsvc.Service, Status, Env config.Env, HTTP *http.Client, Assets, Log, Now}`; `api.Recomputer.RecomputeRecent(ctx) (int, error)`
  - API endpoints from the spec (`/api/settings`, `/api/settings/undo`, `/api/settings/history`, `/api/settings/test`, `/api/suggestions`, `/api/suggestions/apply`, `/api/suggestions/dismiss`, `/api/categories`); `/api/rules` removed.

- [ ] **Step 1: Rewrite config as env-only, test first**

`internal/config/config_test.go` (replace the file):
```go
package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	e, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := Env{DataDir: "./data", Listen: ":8080", LogFormat: "text", MydealzURL: "https://www.mydealz.de",
		PollTrending: 5 * time.Minute, PollNew: 2 * time.Minute}
	if e != want {
		t.Errorf("env = %+v, want %+v", e, want)
	}
}

func TestOverrides(t *testing.T) {
	e, err := FromEnv(env(map[string]string{
		"DEALSIEVE_DATA_DIR": "/data", "DEALSIEVE_LISTEN": "127.0.0.1:9000", "DEALSIEVE_LOG_FORMAT": "json",
		"DEALSIEVE_TELEGRAM_TOKEN": "tok", "DEALSIEVE_NTFY_TOKEN": "nt", "DEALSIEVE_MYDEALZ_URL": "http://127.0.0.1:1",
		"DEALSIEVE_POLL_TRENDING": "10m", "DEALSIEVE_POLL_NEW": "1m",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Env{DataDir: "/data", Listen: "127.0.0.1:9000", LogFormat: "json", TelegramToken: "tok", NtfyToken: "nt",
		MydealzURL: "http://127.0.0.1:1", PollTrending: 10 * time.Minute, PollNew: time.Minute}
	if e != want {
		t.Errorf("env = %+v, want %+v", e, want)
	}
}

func TestErrors(t *testing.T) {
	for in, want := range map[string]string{
		"DEALSIEVE_POLL_TRENDING=30s": "DEALSIEVE_POLL_TRENDING: must be at least 1m0s",
		"DEALSIEVE_POLL_NEW=soon":     "DEALSIEVE_POLL_NEW",
		"DEALSIEVE_LOG_FORMAT=xml":    "DEALSIEVE_LOG_FORMAT",
	} {
		k, v, _ := strings.Cut(in, "=")
		_, err := FromEnv(env(map[string]string{k: v}))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to contain %q", in, err, want)
		}
	}
}
```

Run `go test ./internal/config/` → FAIL (`undefined: FromEnv`). Then replace `internal/config/config.go`:
```go
// Package config reads the settings that come from environment variables:
// paths, the listen address, polling intervals and secrets. Everything the
// user edits lives in the settings package and on the Settings page.
package config

import (
	"errors"
	"fmt"
	"time"
)

const MinPollInterval = time.Minute

type Env struct {
	DataDir       string
	Listen        string
	LogFormat     string
	TelegramToken string
	NtfyToken     string
	MydealzURL    string
	PollTrending  time.Duration
	PollNew       time.Duration
}

func FromEnv(getenv func(string) string) (Env, error) {
	or := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	var errs []error
	poll := func(key string, def time.Duration) time.Duration {
		v := getenv(key)
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			return def
		}
		if d < MinPollInterval {
			errs = append(errs, fmt.Errorf("%s: must be at least %s", key, MinPollInterval))
		}
		return d
	}
	e := Env{
		DataDir:       or("DEALSIEVE_DATA_DIR", "./data"),
		Listen:        or("DEALSIEVE_LISTEN", ":8080"),
		LogFormat:     or("DEALSIEVE_LOG_FORMAT", "text"),
		TelegramToken: getenv("DEALSIEVE_TELEGRAM_TOKEN"),
		NtfyToken:     getenv("DEALSIEVE_NTFY_TOKEN"),
		MydealzURL:    or("DEALSIEVE_MYDEALZ_URL", "https://www.mydealz.de"),
		PollTrending:  poll("DEALSIEVE_POLL_TRENDING", 5*time.Minute),
		PollNew:       poll("DEALSIEVE_POLL_NEW", 2*time.Minute),
	}
	if e.LogFormat != "text" && e.LogFormat != "json" {
		errs = append(errs, fmt.Errorf("DEALSIEVE_LOG_FORMAT: must be text or json, got %q", e.LogFormat))
	}
	return e, errors.Join(errs...)
}
```
Delete `internal/config/duration.go`. The config tests pass now; the rest of the module doesn't compile until the next steps.

- [ ] **Step 2: rules uses settings.Rules**

In `internal/rules/rules.go`: import `github.com/MorrisMorrison/dealsieve/internal/settings` instead of `internal/config`; change the signature to `func New(r settings.Rules, watchlist []string) *Engine` and the window line to `window: r.Recurrence.Window(),`. In `internal/rules/rules_test.go`: `config.Rules` → `settings.Rules`, and `Recurrence: config.Recurrence{Window: config.Duration(90 * 24 * time.Hour), MaxSeen: 2}` → `Recurrence: settings.Recurrence{WindowDays: 90, MaxSeen: 2}` (drop the now-unused imports). Run `go test ./internal/rules/` → `ok`.

- [ ] **Step 3: notify builds notifiers from settings**

Replace `FromConfig` in `internal/notify/notify.go` with:
```go
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
```
(import `internal/settings`; keep `internal/config` for `Env`). In `notify_test.go` rename `TestFromConfig` → `TestFromSettings`, call `FromSettings(map[string]settings.Channel{…}, config.Env{…}, http.DefaultClient)` with the same expectations, and add `if ForChannel(settings.Channel{Type: "email"}, config.Env{}, nil) != nil { t.Error("unknown type") }`. Run `go test ./internal/notify/` → `ok`.

- [ ] **Step 4: pipeline applies settings live**

In `internal/pipeline/pipeline.go`:
- fields: replace `Notify []config.NotifyRule` with `Notify []settings.NotifyRule`; add `Env config.Env` and `HTTP *http.Client` (after `Notifiers`).
- add:
```go
// ApplySettings swaps the rule engine and notifiers in place; a poll in
// flight finishes with the old ones.
func (p *Pipeline) ApplySettings(s settings.Settings) {
	engine := rules.New(s.Rules, s.Watchlist)
	notifiers := notify.FromSettings(s.Channels, p.Env, p.HTTP)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Engine, p.Notify, p.Notifiers = engine, s.Notify, notifiers
}
```
- `RecomputeRecent` returns `(int, error)`: `return len(verdicts), p.Store.SaveVerdicts(ctx, verdicts)` (and `0, err` on the early returns).
- `actionFacts`: remove the `mutedCategories`/`mutedMerchants` maps and their two `case` branches (muting lives in the settings now); pass nothing for `rules.Facts.MutedCategories/MutedMerchants` (they stay nil, which the engine treats as "none").

In `internal/pipeline/pipeline_test.go`: `config.Rules`/`config.Recurrence`/`config.NotifyRule`/`config.When` → `settings.*` (`Recurrence{WindowDays: 90, MaxSeen: 2}`), including the harness signature, which becomes `newHarness(t *testing.T, when settings.When)`; calls `h.p.RecomputeRecent(ctx)` become `if _, err := h.p.RecomputeRecent(ctx); err != nil`; replace `TestMuteCategoryActionIsCaseInsensitive` with this test (muting now comes from settings):
```go
func TestApplySettingsTakesEffect(t *testing.T) {
	h := newHarness(t, settings.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(150)))
	s := settings.Defaults()
	s.Rules.MutedCategories = []string{"ELEKTRONIK"}
	h.p.ApplySettings(s)
	if _, err := h.p.RecomputeRecent(ctx); err != nil {
		t.Fatal(err)
	}
	if v := h.verdict(t, "mydealz:1"); v.Decision != "block" || v.Reasons[0] != "muted category: Elektronik" {
		t.Errorf("verdict = %+v, want blocked by the new muted category", v)
	}
}
```
Run `go test ./internal/pipeline/` → `ok`.

- [ ] **Step 5: API: settings, suggestions and categories endpoints**

Change `internal/api/api.go`:
- `Recomputer` → `RecomputeRecent(ctx context.Context) (int, error)`; its two callers become `if _, err := s.Recompute.RecomputeRecent(r.Context()); err != nil`.
- `Server`: remove `Config *config.Config`; add `Settings *settingsvc.Service`, `Env config.Env`, `HTTP *http.Client`.
- Routes: remove `GET /api/rules` and its handler; add
```go
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("POST /api/settings/undo", s.undoSettings)
	mux.HandleFunc("GET /api/settings/history", s.settingsHistory)
	mux.HandleFunc("POST /api/settings/test", s.testChannel)
	mux.HandleFunc("GET /api/suggestions", s.suggestions)
	mux.HandleFunc("POST /api/suggestions/apply", s.applySuggestion)
	mux.HandleFunc("POST /api/suggestions/dismiss", s.dismissSuggestion)
	mux.HandleFunc("GET /api/categories", s.categories)
```
- `addAction`: right after computing `value` (and the empty-value check), route mutes to the settings:
```go
	if req.Kind == store.ActionMuteCategory || req.Kind == store.ActionMuteMerchant {
		res, err := s.Settings.Mute(r.Context(), req.Kind, value, " (from a deal)")
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"kind": req.Kind, "value": value, "version": res.Version})
		return
	}
```

Create `internal/api/settings.go`:
```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/settingsvc"
	"github.com/MorrisMorrison/dealsieve/internal/suggest"
)

const settingsBodyLimit = 64 << 10

func (s *Server) settingsBody(snap settingsvc.Snapshot) map[string]any {
	return map[string]any{
		"version":    snap.Version,
		"updated_at": snap.UpdatedAt,
		"settings":   snap.Settings,
		"warnings":   settings.Warnings(snap.Settings, s.Env.TelegramToken != ""),
	}
}

func (s *Server) writeResult(w http.ResponseWriter, res settingsvc.Result) {
	body := s.settingsBody(res.Snapshot)
	body["rechecked"], body["changed"] = res.Rechecked, res.Changed
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsBody(s.Settings.Current()))
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version  int64           `json:"version"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, settingsBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	next, err := settings.Decode(req.Settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid settings: "+err.Error())
		return
	}
	res, err := s.Settings.Save(r.Context(), req.Version, next)
	s.respondSave(w, res, err)
}

func (s *Server) undoSettings(w http.ResponseWriter, r *http.Request) {
	res, err := s.Settings.Undo(r.Context())
	s.respondSave(w, res, err)
}

func (s *Server) respondSave(w http.ResponseWriter, res settingsvc.Result, err error) {
	var verr settingsvc.ValidationError
	switch {
	case errors.Is(err, settingsvc.ErrConflict), errors.Is(err, settingsvc.ErrNothingToUndo):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &verr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": verr})
	case err != nil:
		s.fail(w, err)
	default:
		s.writeResult(w, res)
	}
}

func (s *Server) settingsHistory(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Settings.History(r.Context(), 20)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": entries})
}

// testChannel sends a test message through a channel as given in the form,
// before it is saved.
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel settings.Channel `json:"channel"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ch := settings.Settings{Channels: map[string]settings.Channel{"test": req.Channel}}.Normalize().Channels["test"]
	if errs := settings.ValidateChannel("test", ch); len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	if warn := settings.Warnings(settings.Settings{Channels: map[string]settings.Channel{"test": ch}}, s.Env.TelegramToken != ""); warn["test"] != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": warn["test"]})
		return
	}
	err := notify.ForChannel(ch, s.Env, s.HTTP).Send(r.Context(), notify.Message{
		Title: "dealsieve test notification",
		Body:  "If you can read this, the channel works.",
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) suggestions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Settings.Suggestions(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": list})
}

type suggestionRequest struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func decodeSuggestion(w http.ResponseWriter, r *http.Request) (suggestionRequest, bool) {
	var req suggestionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return req, false
	}
	if (req.Kind != suggest.KindMuteMerchant && req.Kind != suggest.KindMuteCategory) || req.Value == "" {
		writeError(w, http.StatusBadRequest, "kind must be mute_merchant or mute_category, with a value")
		return req, false
	}
	return req, true
}

func (s *Server) applySuggestion(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeSuggestion(w, r)
	if !ok {
		return
	}
	if _, err := s.Settings.ApplySuggestion(r.Context(), req.Kind, req.Value); err != nil {
		s.fail(w, err)
		return
	}
	s.suggestions(w, r)
}

func (s *Server) dismissSuggestion(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeSuggestion(w, r)
	if !ok {
		return
	}
	if err := s.Settings.Dismiss(r.Context(), req.Kind, req.Value); err != nil {
		s.fail(w, err)
		return
	}
	s.suggestions(w, r)
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Categories(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": list})
}
```

Update `internal/api/api_test.go`'s harness: drop `Config`; build a service and pass it:
```go
	rc := &fakeRecompute{}
	svc := &settingsvc.Service{Store: st, Apply: rc, Now: func() time.Time { return now }}
	if err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st, Recompute: rc, Settings: svc, Status: …, Env: config.Env{}, HTTP: http.DefaultClient, Assets: …, Log: …, Now: …}
```
with `fakeRecompute` gaining `func (f *fakeRecompute) ApplySettings(settings.Settings) { f.applied++ }` and `RecomputeRecent(ctx) (int, error) { f.calls++; return 7, nil }`. Expose `svc` on the harness. Fix the existing tests: `TestAddAndDeleteAction` — a `mute_category` POST now returns `{"kind","value","version"}` and adds to `svc.Current().Settings.Rules.MutedCategories` (assert that; recompute call counts change accordingly — count the calls the test actually makes); `TestRulesStatusHealthAndPage` drops the `/api/rules` part (keep status/health/page).

Create `internal/api/settings_test.go`:
```go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetAndPutSettings(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/api/settings", "")
	if resp.StatusCode != 200 {
		t.Fatalf("GET %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Version  int64          `json:"version"`
		Settings map[string]any `json:"settings"`
	}
	json.Unmarshal(body, &got)

	const tpl = `{"version":%d,"settings":{"watchlist":["oled"],"rules":{"min_temperature":80,"category_overrides":{},"muted_categories":[],"muted_merchants":[],"block_keywords":[],"recurrence":{"window_days":%d,"max_seen":2}},"channels":{},"notify":[],"suggestions":{"min_count":3,"window_days":30}}}`
	put := fmt.Sprintf(tpl, got.Version, 90)
	resp, body = h.do(t, "PUT", "/api/settings", put)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"rechecked":7`) || !strings.Contains(string(body), `"changed":true`) {
		t.Fatalf("PUT %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "PUT", "/api/settings", put); resp.StatusCode != http.StatusConflict {
		t.Errorf("stale PUT = %d, want 409", resp.StatusCode)
	}

	bad := fmt.Sprintf(tpl, h.svc.Current().Version, 0)
	resp, body = h.do(t, "PUT", "/api/settings", bad)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), `"rules.recurrence.window_days"`) {
		t.Errorf("invalid PUT %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "PUT", "/api/settings", `{"version":1,"settings":{"nope":1}}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown key = %d, want 400", resp.StatusCode)
	}
}

func TestUndoAndHistory(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.do(t, "POST", "/api/settings/undo", ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("undo with nothing to undo = %d", resp.StatusCode)
	}
	h.do(t, "POST", "/api/actions", `{"kind":"mute_category","deal_id":"mydealz:a"}`)
	resp, body := h.do(t, "GET", "/api/settings/history", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `Added muted category \"Elektronik\" (from a deal)`) {
		t.Errorf("history %d: %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, "POST", "/api/settings/undo", ""); resp.StatusCode != 200 || !strings.Contains(string(body), `"muted_categories":[]`) {
		t.Errorf("undo %d: %s", resp.StatusCode, body)
	}
}

func TestTestChannel(t *testing.T) {
	h := newHarness(t)
	var got map[string]any
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer ntfy.Close()
	resp, body := h.do(t, "POST", "/api/settings/test", fmt.Sprintf(`{"channel":{"type":"ntfy","url":%q,"topic":"deals"}}`, ntfy.URL))
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) || got["title"] != "dealsieve test notification" {
		t.Errorf("test send %d: %s (ntfy got %v)", resp.StatusCode, body, got)
	}
	resp, body = h.do(t, "POST", "/api/settings/test", `{"channel":{"type":"telegram","chat_id":"1"}}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "DEALSIEVE_TELEGRAM_TOKEN") {
		t.Errorf("telegram without token %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "POST", "/api/settings/test", `{"channel":{"type":"ntfy"}}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("invalid channel = %d", resp.StatusCode)
	}
}

func TestSuggestionsAndCategories(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/api/suggestions", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"suggestions":[]`) {
		t.Errorf("suggestions %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "POST", "/api/suggestions/apply", `{"kind":"nope","value":"x"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad kind = %d", resp.StatusCode)
	}
	resp, body = h.do(t, "POST", "/api/suggestions/apply", `{"kind":"mute_merchant","value":"Example Shop"}`)
	if resp.StatusCode != 200 || !strings.Contains(fmt.Sprint(h.svc.Current().Settings.Rules.MutedMerchants), "Example Shop") {
		t.Errorf("apply %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "POST", "/api/suggestions/dismiss", `{"kind":"mute_category","value":"Reisen"}`); resp.StatusCode != 200 {
		t.Errorf("dismiss = %d", resp.StatusCode)
	}
	resp, body = h.do(t, "GET", "/api/categories", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"Elektronik"`) {
		t.Errorf("categories %d: %s", resp.StatusCode, body)
	}
}
```
(Adjust the deal IDs and category names to the harness's seed data, which seeds `mydealz:a` in `Elektronik`; keep the assertions' intent.) Run `go test ./internal/api/` → `ok`.

- [ ] **Step 6: main.go loads settings**

In `cmd/dealsieve/main.go`, replace the config loading and pipeline construction with:
```go
	env, err := config.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(env.LogFormat)
	// … MkdirAll(env.DataDir), store.Open as before …
	httpc := &http.Client{Timeout: 30 * time.Second}
	pipe := &pipeline.Pipeline{Store: st, Env: env, HTTP: httpc, Now: time.Now, Log: log}
	svc := &settingsvc.Service{Store: st, Apply: pipe, Now: time.Now}
	if err := svc.Load(ctx); err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
```
then `if _, err := pipe.RecomputeRecent(ctx); err != nil`, poller intervals `env.PollTrending` / `env.PollNew`, `cfg.Env.*` → `env.*`, and the API server gets `Settings: svc, Env: env, HTTP: httpc` instead of `Config: cfg`. Remove the `rules`/`notify` imports if unused; add `settingsvc`. In `cmd/dealsieve/main_test.go` drop the config file and `DEALSIEVE_CONFIG`; set `DEALSIEVE_POLL_TRENDING=1m` and `DEALSIEVE_POLL_NEW=1m`.

- [ ] **Step 7: e2e seeds settings in the database, then changes them through the API**

Deals that arrive before settings exist can't be notified later (re-checks never notify), so the e2e test writes its settings into the database **before** starting the binary. In `test/e2e/e2e_test.go`:
- remove the YAML config and `DEALSIEVE_CONFIG`; add `DEALSIEVE_POLL_TRENDING=1m`, `DEALSIEVE_POLL_NEW=1m`;
- before `cmd.Start()`:
```go
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dataDir, "dealsieve.db"))
	if err != nil {
		t.Fatal(err)
	}
	seed := settings.Defaults()
	seed.Watchlist = []string{"rtx 5080"}
	seed.Rules.BlockKeywords = []string{"gratisprobe"}
	seed.Channels = map[string]settings.Channel{"push": {Type: "ntfy", URL: ntfy.URL, Topic: "deals", Priority: "high"}}
	seed.Notify = []settings.NotifyRule{{When: settings.When{Watchlist: true}, Channels: []string{"push"}}}
	doc, _ := json.Marshal(seed)
	if _, err := st.SaveSettings(context.Background(), doc, []string{"e2e seed"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	st.Close()
```
  (`DEALSIEVE_DATA_DIR` points at `dataDir`; imports `internal/settings`, `internal/store`, `context`.)
- keep all existing assertions; then add, at the end: `GET /api/settings`, PUT the same settings with `"muted_merchants":["Example Shop"]` added, and assert `mydealz:1000001` (an Example Shop deal) now shows in the Blocked view with reason `muted merchant: Example Shop`.

Run `go test -tags e2e -count=1 ./test/e2e/` → `ok`.

- [ ] **Step 8: Screenshot demo configures through the API**

Delete `screenshots/config.yaml`. Create `screenshots/settings.json` (made-up):
```json
{
  "watchlist": ["oled"],
  "rules": {
    "min_temperature": 100,
    "category_overrides": {},
    "muted_categories": ["Fashion & Accessoires"],
    "muted_merchants": [],
    "block_keywords": ["gratisprobe"],
    "recurrence": { "window_days": 90, "max_seen": 2 }
  },
  "channels": {},
  "notify": [],
  "suggestions": { "min_count": 3, "window_days": 30 }
}
```
In `screenshots/capture.mjs`: drop `DEALSIEVE_CONFIG`, add `DEALSIEVE_POLL_TRENDING: '1m', DEALSIEVE_POLL_NEW: '1m'`; right after `/health` answers, `GET /api/settings` for the version and `PUT /api/settings` with `{version, settings: <settings.json>}` (send `Origin` equal to the app's own origin, or no Origin header, so CrossOriginProtection accepts it); fail the capture if the PUT isn't 200. Update the header comment (`./settings.json` instead of `./config.yaml`). If `.github/workflows/screenshots.yml` mentions `config.yaml`, update it.

- [ ] **Step 9: Tidy, run everything, commit**

Run:
```bash
go mod tidy
go vet ./...
go test ./...
go test -tags e2e -count=1 ./test/e2e/
git grep -n "yaml.v3\|DEALSIEVE_CONFIG\|config.Load\|FromConfig" -- ':!docs'
```
Expected: all tests pass; the grep prints nothing (docs are updated in Task 8). `go.mod` no longer lists `gopkg.in/yaml.v3`. Run the screenshot capture locally once if time allows (`cd screenshots && npm ci && npm run capture` after `go build -o dealsieve.exe ./cmd/dealsieve`); otherwise CI verifies it.

```bash
git add -A internal cmd test screenshots go.mod go.sum .github
git commit -m "feat: run on stored settings instead of config.yaml" -m "config shrinks to environment variables (incl. DEALSIEVE_POLL_TRENDING/NEW); the pipeline applies settings live; new settings, history, undo, test-send, suggestions and categories endpoints; mute actions update the settings.

Refs #21" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The settings page

**Files:**
- Create: `web/settings.html`, `web/static/css/settings.css`, `web/static/js/settings.js`, `web/static/js/settings-model.js`, `web/static/js/settings-model.test.js`
- Modify: `web/embed.go` (embed `settings.html`), `web/embed_test.go`, `web/static/js/api.js`, `web/static/css/styles.css` (add `--warn` / `--warn-soft` tokens)

**Interfaces:**
- Consumes: the Task 5 API (`GET/PUT /api/settings` with `{version, updated_at, settings, warnings}` / `{…, rechecked, changed}`, `422 {errors}`, `409 {error}`; `POST /api/settings/undo`; `GET /api/settings/history` → `{history: [{version, created_at, summary}]}`; `POST /api/settings/test {channel}` → `{ok, error?}`; `GET /api/suggestions`, `POST /api/suggestions/apply|dismiss {kind, value}` → `{suggestions}`; `GET /api/categories` → `{categories}`).
- Produces: `/settings.html`; `settings-model.js` exports `clone(doc)`, `changeCount(saved, draft)`, `addTo(list, value)`, `removeFrom(list, value)`, `nextChannelName(channels, base)`.

Visual reference: the approved prototype (a standalone HTML file kept outside the repo; the implementer is given its path). Read it, don't copy it into the repo. Reuse its layout and CSS rules (`.layout`, `.sidenav`, `.card`, `.field`, `.chips`, `.chip`, `.suggestion`, `.override`, `.channels`, `.channel`, `.rule`, `.history`, `.savebar`, `.toast`, `.banner`, responsive block) in `settings.css`, minus the `.proto-bar` rules and minus the `:root` tokens that `styles.css` already defines. Add the missing tokens to both theme blocks in `styles.css`: light `--warn: #b45309; --warn-soft: #fef3c7;`, dark `--warn: #fbbf24; --warn-soft: #3a2e12;`.

- [ ] **Step 1: Write the failing tests for the pure helpers**

`web/static/js/settings-model.test.js`:
```js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { addTo, changeCount, clone, nextChannelName, removeFrom } from './settings-model.js';

const base = {
  watchlist: ['oled'],
  rules: { min_temperature: 100, category_overrides: {}, muted_categories: [], muted_merchants: [], block_keywords: [], recurrence: { window_days: 90, max_seen: 2 } },
  channels: {}, notify: [], suggestions: { min_count: 3, window_days: 30 },
};

test('clone is deep', () => {
  const c = clone(base);
  c.rules.recurrence.max_seen = 9;
  assert.equal(base.rules.recurrence.max_seen, 2);
});

test('addTo ignores blanks and case-insensitive duplicates', () => {
  assert.deepEqual(addTo(['oled'], '  Lego '), ['oled', 'Lego']);
  assert.deepEqual(addTo(['oled'], 'OLED'), ['oled']);
  assert.deepEqual(addTo(['oled'], '   '), ['oled']);
});

test('removeFrom matches exactly', () => {
  assert.deepEqual(removeFrom(['oled', 'lego'], 'oled'), ['lego']);
});

test('changeCount counts changed top-level sections and list entries', () => {
  const d = clone(base);
  assert.equal(changeCount(base, d), 0);
  d.watchlist = addTo(d.watchlist, 'lego');
  d.rules.min_temperature = 80;
  assert.equal(changeCount(base, d), 2);
  d.watchlist = removeFrom(d.watchlist, 'oled');
  assert.equal(changeCount(base, d), 3);
});

test('nextChannelName picks a free lowercase name', () => {
  assert.equal(nextChannelName({}, 'ntfy'), 'ntfy');
  assert.equal(nextChannelName({ ntfy: {}, 'ntfy-2': {} }, 'ntfy'), 'ntfy-3');
});
```

Run: `node --test web/static/js/settings-model.test.js` → FAIL (module not found).

- [ ] **Step 2: Implement settings-model.js**

`web/static/js/settings-model.js`:
```js
// Pure helpers for the settings page: no DOM, unit-tested with node --test.

export const clone = (doc) => JSON.parse(JSON.stringify(doc));

export function addTo(list, value) {
  const v = String(value).trim();
  if (!v || list.some((x) => x.toLowerCase() === v.toLowerCase())) return list;
  return [...list, v];
}

export const removeFrom = (list, value) => list.filter((x) => x !== value);

// Counts what the save bar reports: each added or removed list entry, and
// each other changed setting.
export function changeCount(saved, draft) {
  let n = 0;
  const lists = [
    [saved.watchlist, draft.watchlist],
    [saved.rules.muted_merchants, draft.rules.muted_merchants],
    [saved.rules.muted_categories, draft.rules.muted_categories],
    [saved.rules.block_keywords, draft.rules.block_keywords],
  ];
  for (const [a, b] of lists) {
    n += b.filter((x) => !a.includes(x)).length + a.filter((x) => !b.includes(x)).length;
  }
  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  if (saved.rules.min_temperature !== draft.rules.min_temperature) n++;
  if (!same(saved.rules.category_overrides, draft.rules.category_overrides)) n++;
  if (!same(saved.rules.recurrence, draft.rules.recurrence)) n++;
  if (!same(saved.channels, draft.channels)) n++;
  if (!same(saved.notify, draft.notify)) n++;
  if (!same(saved.suggestions, draft.suggestions)) n++;
  return n;
}

export function nextChannelName(channels, base) {
  if (!(base in channels)) return base;
  let i = 2;
  while (`${base}-${i}` in channels) i++;
  return `${base}-${i}`;
}
```
Run the node test → 5 pass.

- [ ] **Step 3: Page markup, embed, API client**

- `web/embed.go`: `//go:embed index.html settings.html static`.
- `web/embed_test.go`: add `"settings.html"`, `"static/css/settings.css"`, `"static/js/settings.js"` to the asset list.
- `web/static/js/api.js`: export
```js
export const getSettings = () => request('GET', '/api/settings');
export const putSettings = (version, settings) => request('PUT', '/api/settings', { version, settings });
export const undoSettings = () => request('POST', '/api/settings/undo', {});
export const getHistory = () => request('GET', '/api/settings/history').then((r) => r.history);
export const testChannel = (channel) => request('POST', '/api/settings/test', { channel });
export const getSuggestions = () => request('GET', '/api/suggestions').then((r) => r.suggestions);
export const applySuggestion = (kind, value) => request('POST', '/api/suggestions/apply', { kind, value }).then((r) => r.suggestions);
export const dismissSuggestion = (kind, value) => request('POST', '/api/suggestions/dismiss', { kind, value }).then((r) => r.suggestions);
export const getCategories = () => request('GET', '/api/categories').then((r) => r.categories);
```
  and change `request` so a non-OK response throws an `Error` that also carries `status` and the parsed body (`err.status`, `err.body`), keeping the message as today. `PUT /api/settings`'s 422 needs `err.body.errors`.
- `web/settings.html`: same `<head>` as `index.html` (favicon, `styles.css`, plus `settings.css`, `<script type="module" src="/static/js/settings.js">`). Body: the app header (logo + "dealsieve / Settings", "← Back to deals" link to `/`, theme toggle button `#theme-toggle`), a `.layout` with the `<nav class="sidenav">` (links `#suggestions`, `#watchlist`, `#filters`, `#recurrence`, `#notifications`, `#history`; the Suggestions link has `<span class="badge" id="suggestion-count" hidden>`), and one empty `<section class="card" id="…">` per section with its `<h2>` and lead text exactly as in the prototype. The sections are filled by `settings.js`. Add `#savebar` (count, Discard, Save), `#toast`, and an error `<div class="banner" id="page-error" hidden>`.

- [ ] **Step 4: settings.js controller**

`web/static/js/settings.js` renders each section from the draft document with `createElement`/`textContent`, re-rendering only the section that changed. Required behaviour:
- **Load:** `Promise.all([getSettings(), getSuggestions(), getHistory(), getCategories()])`; keep `saved` (the document), `draft = clone(saved)`, `version`, `warnings`.
- **Chips** (watchlist, muted shops, muted categories, block keywords): Enter adds (`addTo`), × removes (`removeFrom`); the category chip input uses a `<datalist>` from `/api/categories`.
- **Minimum temperature**, **per-category minimums** (rows: category `<select>` from categories plus the row's current value, degrees input, Remove; "+ Add category minimum" adds the first unused category with 0), **recurrence** and **suggestion thresholds** (`<input type="number">`; store `Number(input.value)` in the draft, so an empty field becomes `0` and the server's 422 explains what's allowed).
- **Channels:** a card per channel (name, type pill, ntfy: URL/topic/priority select; Telegram: chat ID), the warning from `warnings[name]`, **Send test** → `testChannel(draft.channels[name])` showing "✓ Delivered" or the returned error inline, **Remove** (also removes the name from every notify rule). "+ Add channel" asks type via two buttons (ntfy / Telegram) and adds `nextChannelName(draft.channels, type)` with empty fields.
- **Notify rules:** rows "When [a watchlist deal passes | a deal reaches a temperature] [N°] → send to [channel chips] [+ channel select] Remove"; "+ Add rule" adds `{when: 'watchlist', channels: []}`. In the document `when` is the string `"watchlist"` or `{min_temperature: N}`.
- **Suggestions:** each with title ("Mute shop “X”?" / "Mute category “X”?"), evidence ("You hid or junked N of its deals since <date>"), up to 3 examples, **Apply** (`applySuggestion`, then reload settings + history, toast "Muted …") and **Dismiss** (`dismissSuggestion`). Update `#suggestion-count`. If there are none, show "No suggestions right now."
- **History:** entries "summary joined with '; '" + time (`toLocaleString`); **Undo last change** → `undoSettings()`, then reload; disabled when there's only one version; on 409 show the message.
- **Save bar:** after every draft change, `n = changeCount(saved, draft)`; show the bar with "N unsaved change(s)" when `n > 0`. **Discard** → `draft = clone(saved)`, re-render. **Save** → `putSettings(version, draft)`: on success set `saved`/`draft`/`version`/`warnings` from the response, re-render, reload history, toast "Saved · N recent deals re-checked"; on 422 show each `errors[path]` under the field that owns it (map paths: `watchlist*` → watchlist section, `rules.min_temperature`, `rules.category_overrides`, `rules.recurrence.*`, `channels.<name>*` → that channel card, `notify[i]*` → that rule row, `suggestions.*`) and scroll to the first; on 409 show the page-error banner "Someone else saved the settings. Reload to see them (your unsaved changes will be lost)." with a Reload button.
- `beforeunload` warns while `changeCount > 0`.
- **Side nav:** highlights the section in view (IntersectionObserver) and scrolls on click.
- Theme toggle via the existing `initTheme`.
- Never `innerHTML` with data.

- [ ] **Step 5: Browser check**

Build (`go build -o dealsieve.exe ./cmd/dealsieve` on Windows), run it against the live feeds with a temp data dir (`DEALSIEVE_DATA_DIR`, `DEALSIEVE_LISTEN=127.0.0.1:8091`), open `/settings.html` and check: load; add/remove a watchlist chip (save bar count); Save (toast, history entry); a validation error (set recurrence days to 0 → message under the field); Undo; add an ntfy channel and a notify rule; Discard; dark mode; 375 px width (no horizontal scroll). Stop the server afterwards. Report what you checked.

- [ ] **Step 6: Run the tests and commit**

```bash
go test ./web/ && node --test web/static/js/*.test.js && go vet ./...
git add web
git commit -m "feat(web): settings page" -m "Refs #21" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Inbox: settings link, suggestion banner, undo Hide/Junk on the deal

The inbox's Rules view (read-only JSON + the clicks list with Undo) goes away. Its two remaining jobs move: the settings live on the Settings page, and undoing Hide/Junk happens on the deal itself.

**Files:**
- Modify: `internal/api/api.go` (+ `api_test.go`): add `POST /api/actions/undo`
- Modify: `web/index.html`, `web/static/js/main.js`, `web/static/js/render.js`, `web/static/js/api.js`, `web/static/css/styles.css`

**Interfaces:**
- Consumes: `store.DeleteActionsByValue` (Task 2), `GET /api/suggestions` (Task 5).
- Produces: `POST /api/actions/undo {kind: "hide_deal"|"junk", deal_id}` → `204` (removes the deal's hide action, or every junk action with the deal's fingerprint, then recomputes); `404` for an unknown deal; `400` for other kinds.

- [ ] **Step 1: API test first**

Add to `internal/api/api_test.go`:
```go
func TestUndoHideAndJunk(t *testing.T) {
	h := newHarness(t)
	h.do(t, "POST", "/api/actions", `{"kind":"hide_deal","deal_id":"mydealz:a"}`)
	h.do(t, "POST", "/api/actions", `{"kind":"junk","deal_id":"mydealz:a"}`)
	for _, kind := range []string{"hide_deal", "junk"} {
		resp, _ := h.do(t, "POST", "/api/actions/undo", `{"kind":"`+kind+`","deal_id":"mydealz:a"}`)
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("undo %s = %d", kind, resp.StatusCode)
		}
	}
	if left, _ := h.st.Actions(context.Background()); len(left) != 0 {
		t.Errorf("actions left = %+v", left)
	}
	if resp, _ := h.do(t, "POST", "/api/actions/undo", `{"kind":"mute_category","deal_id":"mydealz:a"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("undo mute = %d, want 400", resp.StatusCode)
	}
	if resp, _ := h.do(t, "POST", "/api/actions/undo", `{"kind":"junk","deal_id":"mydealz:404"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("undo unknown deal = %d, want 404", resp.StatusCode)
	}
}
```
Run → FAIL (405/404). Implement in `api.go` (route `mux.HandleFunc("POST /api/actions/undo", s.undoAction)`):
```go
// undoAction removes a deal's Hide, or the Junk mark on its fingerprint.
func (s *Server) undoAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string `json:"kind"`
		DealID string `json:"deal_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Kind != store.ActionHide && req.Kind != store.ActionJunk {
		writeError(w, http.StatusBadRequest, "only hide_deal and junk can be undone here")
		return
	}
	deal, err := s.Store.GetDeal(r.Context(), req.DealID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deal not found")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	value := deal.ID
	if req.Kind == store.ActionJunk {
		value = deal.Fingerprint
	}
	if _, err := s.Store.DeleteActionsByValue(r.Context(), req.Kind, value); err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.Recompute.RecomputeRecent(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```
(`h.st` is the harness store; add it to the harness struct if missing.) Run → `ok`.

- [ ] **Step 2: Page changes**

- `index.html`: replace the Rules toggle button with a link `<a href="/settings.html" class="icon-btn" aria-label="Settings" title="Settings">` containing the gear SVG from the prototype; remove `#rules-panel`; add under the toolbar `<div id="suggestion-banner" class="banner" hidden><span></span><a href="/settings.html#suggestions">Review →</a></div>`.
- `api.js`: remove `getRules` and `deleteAction`; add `undoAction(kind, dealId)` → `POST /api/actions/undo`.
- `main.js`: remove `state.rulesOpen`, `setRulesOpen`, the rules toggle, `undo` and the rules branch of `load()`; after loading deals, `getSuggestions()` and show the banner text "1 suggestion to tidy your feed" / "N suggestions to tidy your feed" (hidden when 0; ignore errors).
- `render.js`: remove `renderRules`/`ACTION_LABELS`. In `renderDetail`, for a blocked deal whose first reason is `hidden by you` show an **Unhide** button (`undoAction('hide_deal', id)`), and for `like a deal you marked as junk` a **Not junk** button (`undoAction('junk', id)`), placed before the other actions; both reload the list afterwards via the `onAction` callback path (add an `onUndo(kind, deal)` option).
- `styles.css`: `.banner` styles (as in the prototype) for the inbox.

- [ ] **Step 3: Verify and commit**

`go test ./... && node --test web/static/js/*.test.js`; browser check on a local build: the gear opens Settings and "← Back to deals" returns; Hide a deal → it's in Blocked with **Unhide** → Unhide restores it; Junk → **Not junk** restores; the banner appears after hiding 3 deals of one shop and links to the Suggestions section.

```bash
git add internal/api web
git commit -m "feat(web): settings link, suggestion banner, undo Hide/Junk on the deal" -m "Refs #21" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Docs

**Files:**
- Delete: `config.example.yaml`
- Modify: `README.md`, `CLAUDE.md`, `docs-site/src/content/docs/reference/configuration.md` (or the site's configuration page, wherever it is), `docs-site/src/content/docs/guide/how-it-works.md`, `docs-site/src/content/docs/guide/install-and-run.md` (or equivalent), `docs-site/src/content/docs/guide/notifications.md`, `docs/superpowers/specs/2026-11-03-dealsieve-design.md` (one Amendments bullet pointing to the settings spec)

- [ ] **Step 1: Update the docs**

- README:
  - **Run it:** `docker run` without the config mount; "Open the page and configure everything under **Settings** (the gear)".
  - **Configuration:** env-var table — remove `DEALSIEVE_CONFIG`; add `DEALSIEVE_POLL_TRENDING` (`5m`) and `DEALSIEVE_POLL_NEW` (`2m`), minimum `1m`.
  - **"What it filters" and feedback:** settings, undo on the deal, suggestions.
  - **Breaking-change note** for upgrades from v0.0.1: `config.yaml` is no longer read; re-enter settings on the page.
- `CLAUDE.md`:
  - **Layout table:** `internal/config` (env only), new `internal/settings`, `internal/settingsvc`, `internal/suggest`, `web/settings.html`.
  - **Conventions:** replace the config bullet. "User settings live in the `settings` document (validated in `settings.Validate`, summarised in `settings.Summarize`); every setting needs a default in `settings.Defaults`, a validation rule, a summary line, a form control on the Settings page and a docs-site entry. Env vars are for infrastructure and secrets only."
  - **Commands:** "Running it needs a `config.yaml`" becomes "Running it needs nothing but a data dir; configure it on the Settings page".
- docs site: configuration reference → "Settings page" (every setting, its default and validation) + "Environment variables" table; how-it-works mentions where settings live, suggestions and undo; notifications page explains channels/rules on the Settings page, the test button, and that tokens stay in env vars.
- Delete `config.example.yaml`; `git grep -n "config.example.yaml\|config\.yaml\|DEALSIEVE_CONFIG"` must only show the README's breaking-change note and historical spec/plan docs.

- [ ] **Step 2: Verify and commit**

Run: `cd docs-site && npm ci && npm run build` (links valid), `go test ./...` (the example-config test was deleted in Task 5 along with YAML; make sure nothing references it).

```bash
git add -A README.md CLAUDE.md docs-site docs config.example.yaml
git commit -m "docs: settings page replaces config.yaml" -m "Closes #21" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
