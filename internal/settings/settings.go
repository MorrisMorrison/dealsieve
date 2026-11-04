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
