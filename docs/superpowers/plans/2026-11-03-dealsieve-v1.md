# dealsieve v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build dealsieve v1: a single Go binary that polls mydealz's `trending` and `alles` RSS feeds, filters out noise with rules and recurrence detection, shows the result on an embedded vanilla web page with one-click feedback, and sends ntfy/Telegram notifications.

**Architecture:** `poller` fetches feeds through the `mydealz` adapter and hands deals to `pipeline`, which stores them (`store`, SQLite), fingerprints titles (`fingerprint`), evaluates them (`rules`) and sends notifications (`notify`). `api` serves JSON and the embedded page (`web/`); the page is static HTML + ES modules that only talk to the API. `cmd/dealsieve` only wires things together.

**Tech Stack:** Go 1.26, standard library `net/http`, `modernc.org/sqlite` v1.61.1 (pure Go), `gopkg.in/yaml.v3` v3.0.1, vanilla HTML/CSS/ES modules (no build step), GitHub Actions, Docker (distroless static).

**Spec:** `docs/superpowers/specs/2026-11-03-dealsieve-design.md` (read its "Amendments" section).

## Global Constraints

- Module path `github.com/MorrisMorrison/dealsieve`; `go.mod` says `go 1.26.0`.
- Go dependencies: only `modernc.org/sqlite` v1.61.1 and `gopkg.in/yaml.v3` v3.0.1 (plus what they pull in). No web framework.
- Frontend: plain HTML, CSS and ES modules; no framework, no npm packages, no build step. Build the DOM with `textContent`/attributes, never `innerHTML` with data.
- `CGO_ENABLED=0` must build.
- **The repo will be public.** No personal infrastructure (domains, hosts, homelab names, private registries) anywhere in it. Describe the project as "a deal feed filter" with mydealz as its first source, never as "a mydealz alternative". Test fixtures use made-up deals (`Example Shop`, `Example Air`, `static.example.test`), not copied feed content.
- Run all commands from the repo root. Make sure `go` (1.26+) and `node` (24+) are on PATH.
- Every commit message ends with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Code is `gofmt`-clean; comments are sparse and explain *why*.
- Nothing is pushed to GitHub until Task 13, which needs the user's explicit OK.

## File Structure

```
cmd/dealsieve/main.go                 wiring, healthcheck subcommand
internal/config/config.go             YAML + env loading, validation
internal/config/duration.go           Duration type accepting "90d"
internal/source/source.go             Feed, Deal, Fetcher
internal/source/mydealz/mydealz.go    RSS parser + HTTP client
internal/source/mydealz/testdata/     trending.xml, new.xml (made-up)
internal/fingerprint/fingerprint.go   CleanTitle, Key
internal/store/store.go               Open, migrations
internal/store/migrations/001_init.sql
internal/store/deals.go               deals + temperature snapshots
internal/store/verdicts.go            verdicts + list views
internal/store/actions.go             user actions
internal/store/notifications.go       notification bookkeeping
internal/rules/rules.go               rule engine
internal/judge/judge.go               LLM judge interface (Noop only)
internal/notify/notify.go             Message, Notifier, FromConfig, DealMessage
internal/notify/ntfy.go
internal/notify/telegram.go
internal/pipeline/pipeline.go         ingest → verdict → notify, recompute, prune
internal/poller/poller.go             scheduling, jitter, backoff, status
internal/api/api.go                   JSON API + static files
internal/version/version.go
web/embed.go, web/index.html, web/static/css/styles.css
web/static/js/{main,api,render,sparkline,format,theme}.js (+ *.test.js)
test/e2e/e2e_test.go                  build tag e2e
Dockerfile, .dockerignore, .gitignore, LICENSE, README.md, config.example.yaml
.github/workflows/ci.yml, .github/workflows/docker-publish.yml
```

---

### Task 1: Module scaffold and config

**Files:**
- Create: `go.mod`, `.gitignore`, `internal/config/duration.go`, `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Duration time.Duration` with `func (d Duration) D() time.Duration`, `func ParseDuration(s string) (time.Duration, error)`, YAML unmarshal, JSON marshal (`"90d"` for whole days, else Go format).
  - `type Config struct { Poll Poll; Rules Rules; Watchlist []string; Notify []NotifyRule; Channels map[string]Channel; Env Env }`
  - `type Poll struct { Trending, New Duration }`
  - `type Rules struct { MinTemperature int; CategoryOverrides map[string]int; MutedCategories, MutedMerchants, BlockKeywords []string; Recurrence Recurrence }`
  - `type Recurrence struct { Window Duration; MaxSeen int }`
  - `type NotifyRule struct { When When; Channels []string }`, `type When struct { Watchlist bool; MinTemperature int }`
  - `type Channel struct { Type, URL, Topic, Priority, ChatID string }`
  - `type Env struct { DataDir, Listen, LogFormat, TelegramToken, NtfyToken, MydealzURL string }`
  - `func Load(path string, getenv func(string) string) (*Config, error)`
  - `const MinPollInterval = time.Minute`

- [ ] **Step 1: Create the module and .gitignore**

`go.mod`:
```
module github.com/MorrisMorrison/dealsieve

go 1.26.0
```

`.gitignore`:
```
/data/
/config.yaml
/dealsieve
/dealsieve.exe
```

- [ ] **Step 2: Write the failing tests**

`internal/config/config_test.go`:
```go
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const fullConfig = `
poll:
  trending: 5m
  new: 2m
rules:
  min_temperature: 100
  category_overrides: { "Elektronik": 60 }
  muted_categories: ["Fashion & Accessoires"]
  muted_merchants: [Example Air]
  block_keywords: [gratisprobe]
  recurrence: { window: 30d, max_seen: 3 }
watchlist: [oled, rtx 5080]
notify:
  - when: watchlist
    channels: [ntfy-high, telegram]
  - when: { min_temperature: 500 }
    channels: [ntfy-high]
channels:
  ntfy-high: { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: high }
  telegram: { type: telegram, chat_id: "123" }
`

func TestLoadFullConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, fullConfig), env(map[string]string{"DEALSIEVE_TELEGRAM_TOKEN": "tok"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poll.Trending.D() != 5*time.Minute || cfg.Poll.New.D() != 2*time.Minute {
		t.Errorf("poll = %+v", cfg.Poll)
	}
	if cfg.Rules.MinTemperature != 100 || cfg.Rules.CategoryOverrides["Elektronik"] != 60 {
		t.Errorf("rules = %+v", cfg.Rules)
	}
	if cfg.Rules.Recurrence.Window.D() != 30*24*time.Hour || cfg.Rules.Recurrence.MaxSeen != 3 {
		t.Errorf("recurrence = %+v", cfg.Rules.Recurrence)
	}
	if cfg.Rules.MutedCategories[0] != "Fashion & Accessoires" {
		t.Errorf("muted categories = %v", cfg.Rules.MutedCategories)
	}
	if !cfg.Notify[0].When.Watchlist || cfg.Notify[1].When.MinTemperature != 500 {
		t.Errorf("notify = %+v", cfg.Notify)
	}
	if cfg.Channels["telegram"].ChatID != "123" || cfg.Channels["ntfy-high"].Priority != "high" {
		t.Errorf("channels = %+v", cfg.Channels)
	}
	want := Env{DataDir: "./data", Listen: ":8080", LogFormat: "text", TelegramToken: "tok", MydealzURL: "https://www.mydealz.de"}
	if cfg.Env != want {
		t.Errorf("env = %+v, want %+v", cfg.Env, want)
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poll.Trending.D() != 5*time.Minute || cfg.Poll.New.D() != 2*time.Minute {
		t.Errorf("poll = %+v", cfg.Poll)
	}
	if cfg.Rules.Recurrence.Window.D() != 90*24*time.Hour || cfg.Rules.Recurrence.MaxSeen != 2 {
		t.Errorf("recurrence = %+v", cfg.Rules.Recurrence)
	}
}

func TestPartialRecurrenceKeepsDefaultMaxSeen(t *testing.T) {
	cfg, err := Load(writeConfig(t, "rules:\n  recurrence: { window: 30d }\n"), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules.Recurrence.MaxSeen != 2 {
		t.Errorf("max_seen = %d, want default 2", cfg.Rules.Recurrence.MaxSeen)
	}
}

func TestEnvOverrides(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""), env(map[string]string{
		"DEALSIEVE_DATA_DIR": "/data", "DEALSIEVE_LISTEN": "127.0.0.1:9000", "DEALSIEVE_LOG_FORMAT": "json",
		"DEALSIEVE_NTFY_TOKEN": "nt", "DEALSIEVE_MYDEALZ_URL": "http://127.0.0.1:1234",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Env{DataDir: "/data", Listen: "127.0.0.1:9000", LogFormat: "json", NtfyToken: "nt", MydealzURL: "http://127.0.0.1:1234"}
	if cfg.Env != want {
		t.Errorf("env = %+v, want %+v", cfg.Env, want)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name, body string
		env        map[string]string
		want       string
	}{
		{"unknown key", "rules:\n  min_temp: 5\n", nil, "min_temp"},
		{"interval too short", "poll:\n  trending: 30s\n", nil, "poll.trending"},
		{"bad duration", "poll:\n  new: soon\n", nil, "invalid duration"},
		{"unknown channel type", "channels:\n  x: { type: email }\n", nil, `unknown type "email"`},
		{"ntfy without topic", "channels:\n  x: { type: ntfy, url: https://n.example.com }\n", nil, "needs url and topic"},
		{"bad ntfy priority", "channels:\n  x: { type: ntfy, url: https://n.example.com, topic: t, priority: loud }\n", nil, `unknown priority "loud"`},
		{"telegram without token", "channels:\n  x: { type: telegram, chat_id: \"1\" }\n", nil, "DEALSIEVE_TELEGRAM_TOKEN"},
		{"notify unknown channel", "notify:\n  - when: watchlist\n    channels: [nope]\n", nil, `unknown channel "nope"`},
		{"notify without channels", "notify:\n  - when: watchlist\n", nil, "at least one channel"},
		{"bad condition", "notify:\n  - when: always\n    channels: [x]\nchannels:\n  x: { type: ntfy, url: https://n.example.com, topic: t }\n", nil, `unknown condition "always"`},
		{"bad condition key", "notify:\n  - when: { max_temperature: 5 }\n    channels: [x]\nchannels:\n  x: { type: ntfy, url: https://n.example.com, topic: t }\n", nil, `unknown condition key "max_temperature"`},
		{"bad max_seen", "rules:\n  recurrence: { max_seen: 0 }\n", nil, "max_seen"},
		{"bad log format", "", map[string]string{"DEALSIEVE_LOG_FORMAT": "xml"}, "DEALSIEVE_LOG_FORMAT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body), env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"90d", 90 * 24 * time.Hour, false},
		{"5m", 5 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"xd", 0, true},
		{"-1d", 0, true},
		{"soon", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseDuration(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseDuration(%q) = %v, %v", tt.in, got, err)
		}
	}
}

func TestDurationJSON(t *testing.T) {
	for d, want := range map[Duration]string{
		Duration(90 * 24 * time.Hour): `"90d"`,
		Duration(5 * time.Minute):     `"5m0s"`,
	} {
		got, err := json.Marshal(d)
		if err != nil || string(got) != want {
			t.Errorf("Marshal(%v) = %s, %v; want %s", time.Duration(d), got, err, want)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL (build errors: `undefined: Load`, `undefined: ParseDuration`, …).

- [ ] **Step 4: Implement Duration**

`internal/config/duration.go`:
```go
package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that also accepts whole days ("90d"), because
// recurrence windows are naturally given in days.
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func ParseDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	v := time.Duration(d)
	if v > 0 && v%(24*time.Hour) == 0 {
		return json.Marshal(fmt.Sprintf("%dd", v/(24*time.Hour)))
	}
	return json.Marshal(v.String())
}
```

- [ ] **Step 5: Implement Config**

`internal/config/config.go`:
```go
// Package config loads dealsieve's YAML config and the settings that come
// from environment variables (paths, listen address, secrets).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const MinPollInterval = time.Minute

type Config struct {
	Poll      Poll               `yaml:"poll" json:"poll"`
	Rules     Rules              `yaml:"rules" json:"rules"`
	Watchlist []string           `yaml:"watchlist" json:"watchlist"`
	Notify    []NotifyRule       `yaml:"notify" json:"-"`
	Channels  map[string]Channel `yaml:"channels" json:"-"`
	Env       Env                `yaml:"-" json:"-"`
}

type Poll struct {
	Trending Duration `yaml:"trending" json:"trending"`
	New      Duration `yaml:"new" json:"new"`
}

type Rules struct {
	MinTemperature    int            `yaml:"min_temperature" json:"min_temperature"`
	CategoryOverrides map[string]int `yaml:"category_overrides" json:"category_overrides"`
	MutedCategories   []string       `yaml:"muted_categories" json:"muted_categories"`
	MutedMerchants    []string       `yaml:"muted_merchants" json:"muted_merchants"`
	BlockKeywords     []string       `yaml:"block_keywords" json:"block_keywords"`
	Recurrence        Recurrence     `yaml:"recurrence" json:"recurrence"`
}

type Recurrence struct {
	Window  Duration `yaml:"window" json:"window"`
	MaxSeen int      `yaml:"max_seen" json:"max_seen"`
}

type NotifyRule struct {
	When     When     `yaml:"when"`
	Channels []string `yaml:"channels"`
}

// When is a notify condition: the scalar "watchlist" or {min_temperature: N}.
type When struct {
	Watchlist      bool
	MinTemperature int
}

func (w *When) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value != "watchlist" {
			return fmt.Errorf("line %d: unknown condition %q", n.Line, n.Value)
		}
		w.Watchlist = true
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value != "min_temperature" {
				return fmt.Errorf("line %d: unknown condition key %q", k.Line, k.Value)
			}
			if err := v.Decode(&w.MinTemperature); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("line %d: condition must be \"watchlist\" or a mapping", n.Line)
}

type Channel struct {
	Type     string `yaml:"type"`
	URL      string `yaml:"url"`
	Topic    string `yaml:"topic"`
	Priority string `yaml:"priority"`
	ChatID   string `yaml:"chat_id"`
}

type Env struct {
	DataDir       string
	Listen        string
	LogFormat     string
	TelegramToken string
	NtfyToken     string
	MydealzURL    string
}

func Load(path string, getenv func(string) string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.Env = envFrom(getenv)
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Poll:  Poll{Trending: Duration(5 * time.Minute), New: Duration(2 * time.Minute)},
		Rules: Rules{Recurrence: Recurrence{Window: Duration(90 * 24 * time.Hour), MaxSeen: 2}},
	}
}

func envFrom(getenv func(string) string) Env {
	or := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	return Env{
		DataDir:       or("DEALSIEVE_DATA_DIR", "./data"),
		Listen:        or("DEALSIEVE_LISTEN", ":8080"),
		LogFormat:     or("DEALSIEVE_LOG_FORMAT", "text"),
		TelegramToken: getenv("DEALSIEVE_TELEGRAM_TOKEN"),
		NtfyToken:     getenv("DEALSIEVE_NTFY_TOKEN"),
		MydealzURL:    or("DEALSIEVE_MYDEALZ_URL", "https://www.mydealz.de"),
	}
}

var ntfyPriorities = map[string]bool{"": true, "min": true, "low": true, "default": true, "high": true, "urgent": true}

func (c *Config) validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.Poll.Trending.D() < MinPollInterval {
		add("poll.trending: must be at least %s", MinPollInterval)
	}
	if c.Poll.New.D() < MinPollInterval {
		add("poll.new: must be at least %s", MinPollInterval)
	}
	if c.Rules.Recurrence.Window.D() <= 0 {
		add("rules.recurrence.window: must be positive")
	}
	if c.Rules.Recurrence.MaxSeen < 1 {
		add("rules.recurrence.max_seen: must be at least 1")
	}
	if f := c.Env.LogFormat; f != "text" && f != "json" {
		add("DEALSIEVE_LOG_FORMAT: must be text or json, got %q", f)
	}
	for name, ch := range c.Channels {
		switch ch.Type {
		case "ntfy":
			if ch.URL == "" || ch.Topic == "" {
				add("channel %q: ntfy needs url and topic", name)
			}
			if !ntfyPriorities[ch.Priority] {
				add("channel %q: unknown priority %q", name, ch.Priority)
			}
		case "telegram":
			if ch.ChatID == "" {
				add("channel %q: telegram needs chat_id", name)
			}
			if c.Env.TelegramToken == "" {
				add("channel %q: set DEALSIEVE_TELEGRAM_TOKEN", name)
			}
		default:
			add("channel %q: unknown type %q", name, ch.Type)
		}
	}
	for i, n := range c.Notify {
		if !n.When.Watchlist && n.When.MinTemperature <= 0 {
			add(`notify[%d]: when must be "watchlist" or {min_temperature: N}`, i)
		}
		if len(n.Channels) == 0 {
			add("notify[%d]: needs at least one channel", i)
		}
		for _, ch := range n.Channels {
			if _, ok := c.Channels[ch]; !ok {
				add("notify[%d]: unknown channel %q", i, ch)
			}
		}
	}
	return errors.Join(errs...)
}
```

- [ ] **Step 6: Fetch the YAML dependency and run the tests**

Run: `go get gopkg.in/yaml.v3@v3.0.1 && go mod tidy && go test ./internal/config/`
Expected: `ok  github.com/MorrisMorrison/dealsieve/internal/config`

If `TestPartialRecurrenceKeepsDefaultMaxSeen` fails (yaml.v3 zeroing the nested struct), fix it in `Load` by re-applying the defaults for zero values after decoding (`if cfg.Rules.Recurrence.MaxSeen == 0 { … = 2 }` and the same for `Window`), and drop the "bad max_seen" case's expectation to `max_seen: -1`. Don't change the test's intent.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum .gitignore internal/config
git commit -m "feat(config): load and validate YAML config and env settings" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Source types and the mydealz adapter

**Files:**
- Create: `internal/source/source.go`, `internal/source/mydealz/mydealz.go`, `internal/source/mydealz/testdata/trending.xml`, `internal/source/mydealz/testdata/new.xml`
- Test: `internal/source/mydealz/mydealz_test.go`

**Interfaces:**
- Produces:
  - `type Feed string`; `const FeedTrending Feed = "trending"`, `FeedNew Feed = "new"`
  - `type Deal struct { ID, Source, URL, Title, Merchant, Price, Category, ImageURL string; Temperature *int }` (Title has no temperature prefix; Temperature nil when unknown)
  - `type Fetcher interface { Fetch(ctx context.Context, feed Feed) ([]Deal, error) }`
  - `mydealz.Name = "mydealz"`, `func Parse(r io.Reader) ([]source.Deal, []error, error)`, `type Client struct { BaseURL, UserAgent string; HTTP *http.Client; Log *slog.Logger }` implementing `Fetcher`, `type StatusError struct { Code int }`.

- [ ] **Step 1: Write source.go (types only, nothing to test)**

`internal/source/source.go`:
```go
// Package source defines what a deal source delivers.
package source

import "context"

type Feed string

const (
	// FeedTrending feeds the page.
	FeedTrending Feed = "trending"
	// FeedNew carries every new deal; it's only used for watchlist matching.
	FeedNew Feed = "new"
)

type Deal struct {
	ID          string // "<source>:<id at the source>"
	Source      string
	URL         string
	Title       string // without the temperature prefix
	Merchant    string
	Price       string // as the source shows it, e.g. "19,99€"
	Category    string
	ImageURL    string
	Temperature *int // nil when the feed doesn't carry one
}

type Fetcher interface {
	Fetch(ctx context.Context, feed Feed) ([]Deal, error)
}
```

- [ ] **Step 2: Write the fixtures**

`internal/source/mydealz/testdata/trending.xml`:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:media="http://search.yahoo.com/mrss/" xmlns:pepper="http://www.pepper.com/rss" version="2.0">
<channel>
<title>mydealz</title>
<link>https://www.mydealz.de/trending</link>
<item>
<category><![CDATA[Elektronik]]></category>
<pepper:merchant name="Example Shop" price="899€"/>
<media:content medium="image" url="https://static.example.test/1000001.jpg" width="100" height="100"/>
<title><![CDATA[142° - Example OLED TV 55 Zoll]]></title>
<description><![CDATA[<strong>899€ - Example Shop</strong>]]></description>
<link>https://www.mydealz.de/deals/example-oled-tv-55-zoll-1000001</link>
<pubDate>Tue, 03 Nov 2026 15:55:50 +0100</pubDate>
<guid>https://www.mydealz.de/deals/example-oled-tv-55-zoll-1000001</guid>
</item>
<item>
<category><![CDATA[Dienstleistungen & Verträge]]></category>
<pepper:merchant price="0,01€"/>
<title><![CDATA[102° - Lokal: Döner für 1 Cent am Donnerstag]]></title>
<link>https://www.mydealz.de/deals/lokal-doner-fur-1-cent-1000002</link>
</item>
<item>
<category><![CDATA[Reisen]]></category>
<title><![CDATA[-15° - Example Air Flash Sale: Flüge ab 19,99€]]></title>
<link>https://www.mydealz.de/deals/example-air-flash-sale-1000003</link>
</item>
<item>
<category><![CDATA[Elektronik]]></category>
<title><![CDATA[120° - Item without a thread id]]></title>
<link>https://www.mydealz.de/deals/no-thread-id-here</link>
</item>
</channel>
</rss>
```

`internal/source/mydealz/testdata/new.xml`:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:media="http://search.yahoo.com/mrss/" xmlns:pepper="http://www.pepper.com/rss" version="2.0">
<channel>
<title>mydealz</title>
<link>https://www.mydealz.de</link>
<item>
<category><![CDATA[Elektronik]]></category>
<pepper:merchant name="Example Shop" price="999€"/>
<media:content medium="image" url="https://static.example.test/1000010.jpg" width="100" height="100"/>
<title><![CDATA[Example RTX 5080 Grafikkarte 16GB]]></title>
<link>https://www.mydealz.de/deals/example-rtx-5080-grafikkarte-1000010</link>
</item>
<item>
<category><![CDATA[Beauty & Gesundheit]]></category>
<title><![CDATA[Gratisprobe Example Handcreme]]></title>
<link>https://www.mydealz.de/deals/gratisprobe-example-handcreme-1000011</link>
</item>
<item>
<title><![CDATA[Bad link]]></title>
<link>javascript:alert(1)</link>
</item>
</channel>
</rss>
```

- [ ] **Step 3: Write the failing tests**

`internal/source/mydealz/mydealz_test.go`:
```go
package mydealz

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func parseFile(t *testing.T, name string) ([]source.Deal, []error) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	deals, itemErrs, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return deals, itemErrs
}

func TestParseTrending(t *testing.T) {
	deals, itemErrs := parseFile(t, "trending.xml")
	if len(deals) != 3 || len(itemErrs) != 1 {
		t.Fatalf("got %d deals, %d item errors (%v); want 3 and 1", len(deals), len(itemErrs), itemErrs)
	}
	d := deals[0]
	if d.ID != "mydealz:1000001" || d.Source != "mydealz" || d.Title != "Example OLED TV 55 Zoll" ||
		d.Merchant != "Example Shop" || d.Price != "899€" || d.Category != "Elektronik" ||
		d.ImageURL != "https://static.example.test/1000001.jpg" ||
		d.URL != "https://www.mydealz.de/deals/example-oled-tv-55-zoll-1000001" {
		t.Errorf("deal 0 = %+v", d)
	}
	if d.Temperature == nil || *d.Temperature != 142 {
		t.Errorf("deal 0 temperature = %v, want 142", d.Temperature)
	}
	if d := deals[1]; d.Merchant != "" || d.Price != "0,01€" || d.ImageURL != "" || d.Category != "Dienstleistungen & Verträge" {
		t.Errorf("deal 1 = %+v", d)
	}
	if d := deals[2]; d.Temperature == nil || *d.Temperature != -15 || d.Title != "Example Air Flash Sale: Flüge ab 19,99€" {
		t.Errorf("deal 2 = %+v (temperature %v)", d, d.Temperature)
	}
}

func TestParseNewHasNoTemperatures(t *testing.T) {
	deals, itemErrs := parseFile(t, "new.xml")
	if len(deals) != 2 || len(itemErrs) != 1 {
		t.Fatalf("got %d deals, %d item errors; want 2 and 1", len(deals), len(itemErrs))
	}
	for _, d := range deals {
		if d.Temperature != nil {
			t.Errorf("%s: temperature = %d, want nil", d.ID, *d.Temperature)
		}
	}
	if deals[0].Title != "Example RTX 5080 Grafikkarte 16GB" {
		t.Errorf("title = %q", deals[0].Title)
	}
}

func TestParseRejectsHTML(t *testing.T) {
	_, _, err := Parse(strings.NewReader(`<!DOCTYPE html><html><head><title>Redirecting</title></head><body></body></html>`))
	if err == nil {
		t.Fatal("want an error for an HTML page")
	}
}

func newClient(url string, hc *http.Client) *Client {
	return &Client{BaseURL: url, UserAgent: "dealsieve-test", HTTP: hc, Log: slog.New(slog.DiscardHandler)}
}

func TestFetchUsesFeedPathAndUserAgent(t *testing.T) {
	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA = r.URL.Path, r.UserAgent()
		http.ServeFile(w, r, "testdata/trending.xml")
	}))
	defer srv.Close()

	deals, err := newClient(srv.URL, srv.Client()).Fetch(context.Background(), source.FeedTrending)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/rss/trending" || gotUA != "dealsieve-test" || len(deals) != 3 {
		t.Errorf("path %q, UA %q, %d deals", gotPath, gotUA, len(deals))
	}
}

func TestFetchNewUsesAllesFeed(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		http.ServeFile(w, r, "testdata/new.xml")
	}))
	defer srv.Close()

	if _, err := newClient(srv.URL, srv.Client()).Fetch(context.Background(), source.FeedNew); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/rss/alles" {
		t.Errorf("path = %q, want /rss/alles", gotPath)
	}
}

func TestFetchStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := newClient(srv.URL, srv.Client()).Fetch(context.Background(), source.FeedTrending)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want StatusError 503", err)
	}
}

func TestFetchUnknownFeed(t *testing.T) {
	if _, err := newClient("http://127.0.0.1:1", http.DefaultClient).Fetch(context.Background(), "hot"); err == nil {
		t.Fatal("want an error for an unknown feed")
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/source/...`
Expected: FAIL (`undefined: Parse`, `undefined: Client`, …).

- [ ] **Step 5: Implement the adapter**

`internal/source/mydealz/mydealz.go`:
```go
// Package mydealz reads deals from mydealz's public RSS feeds.
package mydealz

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

const Name = "mydealz"

// /rss/hot and /rss/new only redirect to these two.
var feedPaths = map[source.Feed]string{
	source.FeedTrending: "/rss/trending",
	source.FeedNew:      "/rss/alles",
}

var (
	temperaturePrefix = regexp.MustCompile(`^\s*(-?\d+)°\s*-\s*`)
	threadID          = regexp.MustCompile(`-(\d+)/?$`)
)

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Items   []item   `xml:"channel>item"`
}

type item struct {
	Title    string `xml:"title"`
	Link     string `xml:"link"`
	Category string `xml:"category"`
	Merchant struct {
		Name  string `xml:"name,attr"`
		Price string `xml:"price,attr"`
	} `xml:"http://www.pepper.com/rss merchant"`
	Media struct {
		URL string `xml:"url,attr"`
	} `xml:"http://search.yahoo.com/mrss/ content"`
}

// Parse reads a feed. Items that can't be used are reported in the second
// return value and skipped; the error is for a document that isn't a feed.
func Parse(r io.Reader) ([]source.Deal, []error, error) {
	var doc rss
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("decode feed: %w", err)
	}
	var deals []source.Deal
	var itemErrs []error
	for i, it := range doc.Items {
		d, err := toDeal(it)
		if err != nil {
			itemErrs = append(itemErrs, fmt.Errorf("item %d: %w", i, err))
			continue
		}
		deals = append(deals, d)
	}
	return deals, itemErrs, nil
}

func toDeal(it item) (source.Deal, error) {
	link := strings.TrimSpace(it.Link)
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return source.Deal{}, fmt.Errorf("bad link %q", link)
	}
	m := threadID.FindStringSubmatch(u.Path)
	if m == nil {
		return source.Deal{}, fmt.Errorf("no thread id in link %q", link)
	}
	title := strings.TrimSpace(it.Title)
	var temperature *int
	if tm := temperaturePrefix.FindStringSubmatch(title); tm != nil {
		t, err := strconv.Atoi(tm[1])
		if err != nil {
			return source.Deal{}, fmt.Errorf("bad temperature in %q", title)
		}
		temperature = &t
		title = title[len(tm[0]):]
	}
	if title == "" {
		return source.Deal{}, fmt.Errorf("empty title for %q", link)
	}
	return source.Deal{
		ID:          Name + ":" + m[1],
		Source:      Name,
		URL:         link,
		Title:       title,
		Merchant:    strings.TrimSpace(it.Merchant.Name),
		Price:       strings.TrimSpace(it.Merchant.Price),
		Category:    strings.TrimSpace(it.Category),
		ImageURL:    strings.TrimSpace(it.Media.URL),
		Temperature: temperature,
	}, nil
}

type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
	Log       *slog.Logger
}

// StatusError is a non-200 response, so the poller can back off.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("unexpected status %d", e.Code) }

func (c *Client) Fetch(ctx context.Context, feed source.Feed) ([]source.Deal, error) {
	path, ok := feedPaths[feed]
	if !ok {
		return nil, fmt.Errorf("unknown feed %q", feed)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Code: resp.StatusCode}
	}
	deals, itemErrs, err := Parse(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	for _, e := range itemErrs {
		c.Log.Warn("skipping feed item", "feed", feed, "err", e)
	}
	return deals, nil
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/source/...`
Expected: `ok  github.com/MorrisMorrison/dealsieve/internal/source/mydealz`

- [ ] **Step 7: Commit**

```bash
git add internal/source
git commit -m "feat(source): parse mydealz trending and alles RSS feeds" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Fingerprinting

**Files:**
- Create: `internal/fingerprint/fingerprint.go`
- Test: `internal/fingerprint/fingerprint_test.go`

**Interfaces:**
- Produces: `func CleanTitle(title string) string`, `func Key(merchant, cleanTitle string) string`

- [ ] **Step 1: Write the failing tests**

`internal/fingerprint/fingerprint_test.go`:
```go
package fingerprint

import "testing"

func TestCleanTitle(t *testing.T) {
	tests := map[string]string{
		"Example Air Flash Sale: Flüge ab 19,99€ – z.B. nach Mailand": "example air flash sale flüge mailand",
		"Example RTX 5080 Grafikkarte 16GB":                          "example rtx grafikkarte",
		"The best deal for you, with free shipping":                  "best deal you free shipping",
		"  ":                                                         "",
	}
	for in, want := range tests {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeySameForRepostedOffer(t *testing.T) {
	a := Key("Example Air", CleanTitle("Example Air Flash Sale: Flüge ab 19,99€"))
	b := Key("example air ", CleanTitle("Example Air – Flash Sale, Flüge ab 14,99 €"))
	if a != b {
		t.Errorf("keys differ: %q vs %q", a, b)
	}
	if want := "example air|air example flash flüge sale"; a != want {
		t.Errorf("key = %q, want %q", a, want)
	}
}

func TestKeyIgnoresWordOrderAndDuplicates(t *testing.T) {
	if Key("Shop", "sale flash sale") != Key("Shop", "flash sale") {
		t.Error("word order or duplicates changed the key")
	}
}

func TestKeyDependsOnMerchant(t *testing.T) {
	if Key("Shop A", "oled tv") == Key("Shop B", "oled tv") {
		t.Error("different merchants gave the same key")
	}
	if got := Key("", "oled tv"); got != "-|oled tv" {
		t.Errorf("missing merchant key = %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/fingerprint/`
Expected: FAIL (`undefined: CleanTitle`).

- [ ] **Step 3: Implement**

`internal/fingerprint/fingerprint.go`:
```go
// Package fingerprint turns deal titles into keys that stay the same when a
// merchant re-posts the same kind of offer with different numbers.
package fingerprint

import (
	"slices"
	"strings"
	"unicode"
)

var stopwords = map[string]bool{
	// German
	"ab": true, "am": true, "an": true, "auf": true, "aus": true, "bei": true, "bis": true,
	"das": true, "dem": true, "den": true, "der": true, "des": true, "die": true, "ein": true,
	"eine": true, "einen": true, "für": true, "im": true, "in": true, "inkl": true, "ist": true,
	"jetzt": true, "mit": true, "nach": true, "nur": true, "oder": true, "statt": true,
	"und": true, "uvp": true, "vom": true, "von": true, "zu": true, "zum": true, "zur": true,
	// English
	"and": true, "at": true, "for": true, "from": true, "of": true, "on": true, "only": true,
	"or": true, "the": true, "to": true, "with": true, "now": true,
}

// CleanTitle lowercases a title and drops numbers, punctuation, one-letter
// words and stopwords.
func CleanTitle(title string) string {
	fields := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	words := fields[:0]
	for _, w := range fields {
		if len([]rune(w)) < 2 || stopwords[w] || strings.ContainsFunc(w, unicode.IsDigit) {
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// Key is the merchant plus the cleaned title's distinct words in sorted order.
func Key(merchant, cleanTitle string) string {
	m := strings.ToLower(strings.TrimSpace(merchant))
	if m == "" {
		m = "-"
	}
	words := strings.Fields(cleanTitle)
	slices.Sort(words)
	return m + "|" + strings.Join(slices.Compact(words), " ")
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/fingerprint/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/fingerprint
git commit -m "feat(fingerprint): normalize titles into recurrence keys" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: SQLite store

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrations/001_init.sql`, `internal/store/deals.go`, `internal/store/verdicts.go`, `internal/store/actions.go`, `internal/store/notifications.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `source.Deal`, `source.Feed` (Task 2).
- Produces:
  - `func Open(path string) (*Store, error)`, `func (s *Store) Close() error`, `var ErrNotFound`
  - `type DealRecord struct { source.Deal; TitleClean, Fingerprint string; FirstSeen, LastSeen time.Time; InTrending, InNew bool }`
  - `func (s *Store) UpsertDeal(ctx context.Context, d source.Deal, feed source.Feed, titleClean, fingerprint string, now time.Time) (DealRecord, error)`
  - `func (s *Store) GetDeal(ctx context.Context, id string) (DealRecord, error)`
  - `func (s *Store) DealsSeenSince(ctx context.Context, since time.Time) ([]DealRecord, error)`
  - `func (s *Store) CountRecurrence(ctx context.Context, fingerprint, excludeID string, since time.Time) (int, time.Time, error)`
  - `func (s *Store) Temperatures(ctx context.Context, ids []string) (map[string][]int, error)`
  - `func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error)`
  - `type Verdict struct { DealID, Decision string; Score int; WatchlistHit bool; Reasons []string; DecidedAt time.Time }`
  - `func (s *Store) SaveVerdict(ctx context.Context, v Verdict) error`, `func (s *Store) GetVerdict(ctx context.Context, dealID string) (Verdict, error)`
  - `const ViewPassed = "passed"`, `ViewWatchlist = "watchlist"`, `ViewBlocked = "blocked"`; `func ValidView(v string) bool`
  - `type Listed struct { DealRecord; Verdict Verdict }`, `func (s *Store) ListDeals(ctx context.Context, view string, since time.Time, limit int) ([]Listed, error)`
  - `type Action struct { ID int64; Kind, Value string; CreatedAt time.Time }` (JSON tags `id`, `kind`, `value`, `created_at`)
  - `const ActionHide = "hide_deal"`, `ActionJunk = "junk"`, `ActionMuteCategory = "mute_category"`, `ActionMuteMerchant = "mute_merchant"`; `func ValidActionKind(k string) bool`
  - `func (s *Store) AddAction(ctx context.Context, kind, value string, now time.Time) (Action, error)`, `func (s *Store) DeleteAction(ctx context.Context, id int64) (bool, error)`, `func (s *Store) Actions(ctx context.Context) ([]Action, error)` (newest first)
  - `func (s *Store) NotificationState(ctx context.Context, dealID, channel string) (sent bool, attempts int, err error)`
  - `func (s *Store) RecordNotification(ctx context.Context, dealID, channel string, ok bool, now time.Time) error`
  - `type PendingNotification struct { DealID, Channel string }`, `func (s *Store) PendingNotifications(ctx context.Context, maxAttempts int) ([]PendingNotification, error)`

Note: the store uses one connection (`SetMaxOpenConns(1)`). Always finish reading `rows` (and close them) or commit a transaction before running the next query, or it will deadlock.

- [ ] **Step 1: Write the failing tests**

`internal/store/store_test.go`:
```go
package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func intp(v int) *int { return &v }

func deal(id string, temp *int) source.Deal {
	return source.Deal{
		ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
		Title: "Deal " + id, Merchant: "Shop", Price: "9€", Category: "Elektronik", Temperature: temp,
	}
}

func mustUpsert(t *testing.T, s *Store, d source.Deal, feed source.Feed, fp string, at time.Time) DealRecord {
	t.Helper()
	rec, err := s.UpsertDeal(ctx, d, feed, "deal", fp, at)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestOpenIsRepeatable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestUpsertMergesFeedsAndKeepsHistory(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("1", intp(100)), source.FeedTrending, "fp", t0)
	rec := mustUpsert(t, s, deal("1", nil), source.FeedNew, "fp", t0.Add(5*time.Minute))
	if !rec.InTrending || !rec.InNew {
		t.Errorf("feeds = trending %v, new %v; want both", rec.InTrending, rec.InNew)
	}
	if !rec.FirstSeen.Equal(t0) || !rec.LastSeen.Equal(t0.Add(5*time.Minute)) {
		t.Errorf("first %v, last %v", rec.FirstSeen, rec.LastSeen)
	}
	if rec.Temperature == nil || *rec.Temperature != 100 {
		t.Errorf("temperature = %v, want 100 kept", rec.Temperature)
	}
	mustUpsert(t, s, deal("1", intp(150)), source.FeedTrending, "fp", t0.Add(10*time.Minute))
	temps, err := s.Temperatures(ctx, []string{"mydealz:1", "mydealz:404"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(temps["mydealz:1"], []int{100, 150}) || len(temps["mydealz:404"]) != 0 {
		t.Errorf("temperatures = %v", temps)
	}
}

func TestGetDealNotFound(t *testing.T) {
	if _, err := openTest(t).GetDeal(ctx, "mydealz:404"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDealsSeenSince(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("old", nil), source.FeedTrending, "a", t0.Add(-10*24*time.Hour))
	mustUpsert(t, s, deal("new", nil), source.FeedTrending, "b", t0)
	recs, err := s.DealsSeenSince(ctx, t0.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].ID != "mydealz:new" {
		t.Errorf("got %v", recs)
	}
}

func TestCountRecurrence(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("a", nil), source.FeedTrending, "shop|x", t0.Add(-100*24*time.Hour))
	mustUpsert(t, s, deal("b", nil), source.FeedTrending, "shop|x", t0.Add(-10*24*time.Hour))
	mustUpsert(t, s, deal("c", nil), source.FeedTrending, "shop|x", t0.Add(-24*time.Hour))
	mustUpsert(t, s, deal("other", nil), source.FeedTrending, "shop|y", t0)
	mustUpsert(t, s, deal("me", nil), source.FeedTrending, "shop|x", t0)

	n, last, err := s.CountRecurrence(ctx, "shop|x", "mydealz:me", t0.Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || !last.Equal(t0.Add(-24*time.Hour)) {
		t.Errorf("count %d, last %v; want 2 and %v", n, last, t0.Add(-24*time.Hour))
	}
	if n, _, _ := s.CountRecurrence(ctx, "shop|none", "x", t0.Add(-90*24*time.Hour)); n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func save(t *testing.T, s *Store, id, decision string, score int, watch bool) {
	t.Helper()
	if err := s.SaveVerdict(ctx, Verdict{DealID: id, Decision: decision, Score: score, WatchlistHit: watch, Reasons: []string{"r-" + id}, DecidedAt: t0}); err != nil {
		t.Fatal(err)
	}
}

func ids(ls []Listed) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.ID)
	}
	return out
}

func TestListDealsViews(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("a", intp(150)), source.FeedTrending, "a", t0) // trending pass
	mustUpsert(t, s, deal("b", nil), source.FeedNew, "b", t0)            // new-only watchlist pass
	mustUpsert(t, s, deal("c", intp(50)), source.FeedTrending, "c", t0)  // trending block
	mustUpsert(t, s, deal("d", nil), source.FeedNew, "d", t0)            // new-only block
	mustUpsert(t, s, deal("e", intp(300)), source.FeedTrending, "e", t0) // trending pass, higher score
	save(t, s, "mydealz:a", "pass", 150, false)
	save(t, s, "mydealz:b", "pass", 1000, true)
	save(t, s, "mydealz:c", "block", 50, false)
	save(t, s, "mydealz:d", "block", 0, false)
	save(t, s, "mydealz:e", "pass", 300, false)

	for view, want := range map[string][]string{
		ViewPassed:    {"mydealz:e", "mydealz:a"},
		ViewWatchlist: {"mydealz:b"},
		ViewBlocked:   {"mydealz:c"},
	} {
		got, err := s.ListDeals(ctx, view, t0.Add(-time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(got), want) {
			t.Errorf("%s = %v, want %v", view, ids(got), want)
		}
	}
	got, _ := s.ListDeals(ctx, ViewBlocked, t0.Add(-time.Hour), 100)
	if v := got[0].Verdict; v.Decision != "block" || v.Score != 50 || !slices.Equal(v.Reasons, []string{"r-mydealz:c"}) {
		t.Errorf("verdict = %+v", v)
	}
	if got, _ := s.ListDeals(ctx, ViewPassed, t0.Add(time.Hour), 100); len(got) != 0 {
		t.Errorf("since filter: got %v", ids(got))
	}
	if _, err := s.ListDeals(ctx, "nope", t0, 10); err == nil {
		t.Error("want an error for an unknown view")
	}
}

func TestGetVerdict(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("a", nil), source.FeedTrending, "a", t0)
	if _, err := s.GetVerdict(ctx, "mydealz:a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	save(t, s, "mydealz:a", "pass", 7, true)
	v, err := s.GetVerdict(ctx, "mydealz:a")
	if err != nil || v.Score != 7 || !v.WatchlistHit || !v.DecidedAt.Equal(t0) {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
}

func TestActions(t *testing.T) {
	s := openTest(t)
	a1, err := s.AddAction(ctx, ActionHide, "mydealz:1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAction(ctx, ActionMuteCategory, "Reisen", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := s.Actions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Kind != ActionMuteCategory || list[1].ID != a1.ID || !list[1].CreatedAt.Equal(t0) {
		t.Errorf("actions = %+v", list)
	}
	if ok, err := s.DeleteAction(ctx, a1.ID); !ok || err != nil {
		t.Errorf("delete = %v, %v", ok, err)
	}
	if ok, _ := s.DeleteAction(ctx, a1.ID); ok {
		t.Error("second delete reported found")
	}
	if !ValidActionKind(ActionJunk) || ValidActionKind("explode") {
		t.Error("ValidActionKind is wrong")
	}
}

func TestNotifications(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("1", nil), source.FeedTrending, "a", t0)
	if err := s.RecordNotification(ctx, "mydealz:1", "push", false, t0); err != nil {
		t.Fatal(err)
	}
	if sent, attempts, _ := s.NotificationState(ctx, "mydealz:1", "push"); sent || attempts != 1 {
		t.Errorf("after failure: sent %v, attempts %d", sent, attempts)
	}
	pending, err := s.PendingNotifications(ctx, 3)
	if err != nil || len(pending) != 1 || pending[0] != (PendingNotification{"mydealz:1", "push"}) {
		t.Errorf("pending = %v, %v", pending, err)
	}
	if err := s.RecordNotification(ctx, "mydealz:1", "push", true, t0); err != nil {
		t.Fatal(err)
	}
	if sent, attempts, _ := s.NotificationState(ctx, "mydealz:1", "push"); !sent || attempts != 2 {
		t.Errorf("after success: sent %v, attempts %d", sent, attempts)
	}
	if pending, _ := s.PendingNotifications(ctx, 3); len(pending) != 0 {
		t.Errorf("pending after success = %v", pending)
	}
	if sent, attempts, err := s.NotificationState(ctx, "mydealz:1", "other"); sent || attempts != 0 || err != nil {
		t.Errorf("unknown channel: %v %d %v", sent, attempts, err)
	}
}

func TestPruneCascades(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("old", intp(10)), source.FeedTrending, "a", t0.Add(-200*24*time.Hour))
	mustUpsert(t, s, deal("new", intp(10)), source.FeedTrending, "b", t0)
	save(t, s, "mydealz:old", "pass", 10, false)
	n, err := s.Prune(ctx, t0.Add(-180*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v; want 1", n, err)
	}
	if _, err := s.GetDeal(ctx, "mydealz:old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old deal still there: %v", err)
	}
	if _, err := s.GetVerdict(ctx, "mydealz:old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old verdict still there: %v", err)
	}
	temps, _ := s.Temperatures(ctx, []string{"mydealz:old"})
	if len(temps["mydealz:old"]) != 0 {
		t.Errorf("old snapshots still there: %v", temps)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL (`undefined: Open`, …).

- [ ] **Step 3: Write the migration**

`internal/store/migrations/001_init.sql`:
```sql
CREATE TABLE deals (
    id          TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    url         TEXT NOT NULL,
    title       TEXT NOT NULL,
    title_clean TEXT NOT NULL,
    merchant    TEXT NOT NULL DEFAULT '',
    price       TEXT NOT NULL DEFAULT '',
    category    TEXT NOT NULL DEFAULT '',
    image_url   TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL,
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL,
    in_trending INTEGER NOT NULL DEFAULT 0,
    in_new      INTEGER NOT NULL DEFAULT 0,
    temperature INTEGER
);
CREATE INDEX deals_fingerprint ON deals (fingerprint, first_seen);
CREATE INDEX deals_last_seen ON deals (last_seen);

CREATE TABLE temperature_snapshots (
    deal_id     TEXT NOT NULL REFERENCES deals (id) ON DELETE CASCADE,
    at          INTEGER NOT NULL,
    temperature INTEGER NOT NULL
);
CREATE INDEX temperature_snapshots_deal ON temperature_snapshots (deal_id, at);

CREATE TABLE verdicts (
    deal_id       TEXT PRIMARY KEY REFERENCES deals (id) ON DELETE CASCADE,
    decision      TEXT NOT NULL,
    score         INTEGER NOT NULL,
    watchlist_hit INTEGER NOT NULL,
    reasons       TEXT NOT NULL,
    decided_at    INTEGER NOT NULL
);

CREATE TABLE actions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    value      TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE notifications_sent (
    deal_id  TEXT NOT NULL REFERENCES deals (id) ON DELETE CASCADE,
    channel  TEXT NOT NULL,
    sent_at  INTEGER,
    attempts INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (deal_id, channel)
);
```

- [ ] **Step 4: Implement store.go**

`internal/store/store.go`:
```go
// Package store keeps deals, their temperature history, verdicts, user
// actions and notification bookkeeping in SQLite.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// One writer keeps SQLite simple; the app's load is tiny.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate applies migrations/NNN_*.sql in order; PRAGMA user_version holds
// how many have run.
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	for i, e := range entries {
		n := i + 1
		if n <= version {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func fromUnix(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
```

- [ ] **Step 5: Implement deals.go**

`internal/store/deals.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

type DealRecord struct {
	source.Deal
	TitleClean  string
	Fingerprint string
	FirstSeen   time.Time
	LastSeen    time.Time
	InTrending  bool
	InNew       bool
}

var feedColumn = map[source.Feed]string{
	source.FeedTrending: "in_trending",
	source.FeedNew:      "in_new",
}

const dealColumns = `d.id, d.source, d.url, d.title, d.title_clean, d.merchant, d.price, d.category,
	d.image_url, d.fingerprint, d.first_seen, d.last_seen, d.in_trending, d.in_new, d.temperature`

type scanner interface{ Scan(dest ...any) error }

func scanDeal(sc scanner, extra ...any) (DealRecord, error) {
	var r DealRecord
	var first, last int64
	var temp sql.NullInt64
	dest := append([]any{&r.ID, &r.Source, &r.URL, &r.Title, &r.TitleClean, &r.Merchant, &r.Price,
		&r.Category, &r.ImageURL, &r.Fingerprint, &first, &last, &r.InTrending, &r.InNew, &temp}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return DealRecord{}, err
	}
	r.FirstSeen, r.LastSeen = fromUnix(first), fromUnix(last)
	if temp.Valid {
		t := int(temp.Int64)
		r.Temperature = &t
	}
	return r, nil
}

// UpsertDeal records that feed showed d at now. first_seen never changes; a
// missing temperature keeps the last known one.
func (s *Store) UpsertDeal(ctx context.Context, d source.Deal, feed source.Feed, titleClean, fingerprint string, now time.Time) (DealRecord, error) {
	col, ok := feedColumn[feed]
	if !ok {
		return DealRecord{}, fmt.Errorf("unknown feed %q", feed)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DealRecord{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO deals (id, source, url, title, title_clean, merchant, price,
			category, image_url, fingerprint, first_seen, last_seen, temperature, `+col+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT (id) DO UPDATE SET url = excluded.url, title = excluded.title,
			title_clean = excluded.title_clean, merchant = excluded.merchant, price = excluded.price,
			category = excluded.category, image_url = excluded.image_url,
			fingerprint = excluded.fingerprint, last_seen = excluded.last_seen,
			temperature = COALESCE(excluded.temperature, deals.temperature), `+col+` = 1`,
		d.ID, d.Source, d.URL, d.Title, titleClean, d.Merchant, d.Price, d.Category, d.ImageURL,
		fingerprint, now.Unix(), now.Unix(), nullInt(d.Temperature))
	if err != nil {
		return DealRecord{}, err
	}
	if d.Temperature != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO temperature_snapshots (deal_id, at, temperature) VALUES (?, ?, ?)`,
			d.ID, now.Unix(), *d.Temperature); err != nil {
			return DealRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return DealRecord{}, err
	}
	return s.GetDeal(ctx, d.ID)
}

func (s *Store) GetDeal(ctx context.Context, id string) (DealRecord, error) {
	rec, err := scanDeal(s.db.QueryRowContext(ctx, `SELECT `+dealColumns+` FROM deals d WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DealRecord{}, ErrNotFound
	}
	return rec, err
}

func (s *Store) DealsSeenSince(ctx context.Context, since time.Time) ([]DealRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dealColumns+` FROM deals d WHERE d.last_seen >= ? ORDER BY d.first_seen, d.id`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DealRecord
	for rows.Next() {
		rec, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CountRecurrence counts other deals with the same fingerprint first seen
// since since, and returns when the latest of them was first seen.
func (s *Store) CountRecurrence(ctx context.Context, fingerprint, excludeID string, since time.Time) (int, time.Time, error) {
	var n int
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(first_seen) FROM deals
		WHERE fingerprint = ? AND id <> ? AND first_seen >= ?`, fingerprint, excludeID, since.Unix()).Scan(&n, &last)
	if err != nil || !last.Valid {
		return n, time.Time{}, err
	}
	return n, fromUnix(last.Int64), nil
}

// Temperatures returns each deal's snapshots, oldest first.
func (s *Store) Temperatures(ctx context.Context, ids []string) (map[string][]int, error) {
	out := make(map[string][]int, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT deal_id, temperature FROM temperature_snapshots
		WHERE deal_id IN (?`+strings.Repeat(", ?", len(ids)-1)+`) ORDER BY deal_id, at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t int
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = append(out[id], t)
	}
	return out, rows.Err()
}

// Prune deletes deals last seen before before, with everything attached.
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM deals WHERE last_seen < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 6: Implement verdicts.go**

`internal/store/verdicts.go`:
```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Verdict struct {
	DealID       string
	Decision     string
	Score        int
	WatchlistHit bool
	Reasons      []string
	DecidedAt    time.Time
}

type Listed struct {
	DealRecord
	Verdict Verdict
}

const (
	ViewPassed    = "passed"
	ViewWatchlist = "watchlist"
	ViewBlocked   = "blocked"
)

// A deal only in the new feed is a page candidate only as a watchlist hit.
var viewFilters = map[string]string{
	ViewPassed:    `v.decision = 'pass' AND d.in_trending = 1`,
	ViewWatchlist: `v.decision = 'pass' AND v.watchlist_hit = 1`,
	ViewBlocked:   `v.decision = 'block' AND (d.in_trending = 1 OR v.watchlist_hit = 1)`,
}

func ValidView(v string) bool {
	_, ok := viewFilters[v]
	return ok
}

func (s *Store) SaveVerdict(ctx context.Context, v Verdict) error {
	reasons := v.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	raw, err := json.Marshal(reasons)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO verdicts (deal_id, decision, score, watchlist_hit, reasons, decided_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (deal_id) DO UPDATE SET decision = excluded.decision, score = excluded.score,
			watchlist_hit = excluded.watchlist_hit, reasons = excluded.reasons, decided_at = excluded.decided_at`,
		v.DealID, v.Decision, v.Score, v.WatchlistHit, string(raw), v.DecidedAt.Unix())
	return err
}

func (s *Store) GetVerdict(ctx context.Context, dealID string) (Verdict, error) {
	v := Verdict{DealID: dealID}
	var reasons string
	var decided int64
	err := s.db.QueryRowContext(ctx, `SELECT decision, score, watchlist_hit, reasons, decided_at FROM verdicts WHERE deal_id = ?`, dealID).
		Scan(&v.Decision, &v.Score, &v.WatchlistHit, &reasons, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return Verdict{}, ErrNotFound
	}
	if err != nil {
		return Verdict{}, err
	}
	v.DecidedAt = fromUnix(decided)
	return v, json.Unmarshal([]byte(reasons), &v.Reasons)
}

// ListDeals returns a view's deals last seen since since, best score first.
func (s *Store) ListDeals(ctx context.Context, view string, since time.Time, limit int) ([]Listed, error) {
	where, ok := viewFilters[view]
	if !ok {
		return nil, fmt.Errorf("unknown view %q", view)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+dealColumns+`, v.decision, v.score, v.watchlist_hit, v.reasons, v.decided_at
		FROM deals d JOIN verdicts v ON v.deal_id = d.id
		WHERE `+where+` AND d.last_seen >= ?
		ORDER BY v.score DESC, d.first_seen DESC, d.id LIMIT ?`, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Listed
	for rows.Next() {
		var l Listed
		var reasons string
		var decided int64
		rec, err := scanDeal(rows, &l.Verdict.Decision, &l.Verdict.Score, &l.Verdict.WatchlistHit, &reasons, &decided)
		if err != nil {
			return nil, err
		}
		l.DealRecord = rec
		l.Verdict.DealID = rec.ID
		l.Verdict.DecidedAt = fromUnix(decided)
		if err := json.Unmarshal([]byte(reasons), &l.Verdict.Reasons); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
```

- [ ] **Step 7: Implement actions.go**

`internal/store/actions.go`:
```go
package store

import (
	"context"
	"time"
)

const (
	ActionHide         = "hide_deal"     // value: deal ID
	ActionJunk         = "junk"          // value: fingerprint
	ActionMuteCategory = "mute_category" // value: category
	ActionMuteMerchant = "mute_merchant" // value: merchant
)

func ValidActionKind(k string) bool {
	switch k {
	case ActionHide, ActionJunk, ActionMuteCategory, ActionMuteMerchant:
		return true
	}
	return false
}

type Action struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) AddAction(ctx context.Context, kind, value string, now time.Time) (Action, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO actions (kind, value, created_at) VALUES (?, ?, ?)`, kind, value, now.Unix())
	if err != nil {
		return Action{}, err
	}
	id, err := res.LastInsertId()
	return Action{ID: id, Kind: kind, Value: value, CreatedAt: fromUnix(now.Unix())}, err
}

func (s *Store) DeleteAction(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM actions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Actions returns all actions, newest first.
func (s *Store) Actions(ctx context.Context) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, value, created_at FROM actions ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var a Action
		var created int64
		if err := rows.Scan(&a.ID, &a.Kind, &a.Value, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = fromUnix(created)
		out = append(out, a)
	}
	return out, rows.Err()
}
```

- [ ] **Step 8: Implement notifications.go**

`internal/store/notifications.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PendingNotification struct {
	DealID  string
	Channel string
}

func (s *Store) NotificationState(ctx context.Context, dealID, channel string) (bool, int, error) {
	var sentAt sql.NullInt64
	var attempts int
	err := s.db.QueryRowContext(ctx, `SELECT sent_at, attempts FROM notifications_sent WHERE deal_id = ? AND channel = ?`, dealID, channel).
		Scan(&sentAt, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	return sentAt.Valid, attempts, err
}

// RecordNotification counts one send attempt; ok marks the deal as sent.
func (s *Store) RecordNotification(ctx context.Context, dealID, channel string, ok bool, now time.Time) error {
	var sentAt any
	if ok {
		sentAt = now.Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO notifications_sent (deal_id, channel, sent_at, attempts) VALUES (?, ?, ?, 1)
		ON CONFLICT (deal_id, channel) DO UPDATE SET attempts = attempts + 1,
			sent_at = COALESCE(excluded.sent_at, notifications_sent.sent_at)`, dealID, channel, sentAt)
	return err
}

func (s *Store) PendingNotifications(ctx context.Context, maxAttempts int) ([]PendingNotification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT deal_id, channel FROM notifications_sent
		WHERE sent_at IS NULL AND attempts < ? ORDER BY deal_id, channel`, maxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingNotification
	for rows.Next() {
		var p PendingNotification
		if err := rows.Scan(&p.DealID, &p.Channel); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
```

- [ ] **Step 9: Fetch the SQLite driver and run the tests**

Run: `go get modernc.org/sqlite@v1.61.1 && go mod tidy && go test ./internal/store/`
Expected: `ok  github.com/MorrisMorrison/dealsieve/internal/store`

- [ ] **Step 10: Commit**

```bash
git add go.mod go.sum internal/store
git commit -m "feat(store): SQLite store for deals, verdicts, actions and notifications" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Rule engine and judge interface

**Files:**
- Create: `internal/rules/rules.go`, `internal/judge/judge.go`
- Test: `internal/rules/rules_test.go`

**Interfaces:**
- Consumes: `config.Rules`, `config.Duration` (Task 1).
- Produces:
  - `type Decision string`; `const Pass Decision = "pass"`, `Block Decision = "block"`
  - `type Deal struct { Title, Merchant, Category, Fingerprint string; Temperature *int; InTrending bool }`
  - `type Facts struct { Hidden bool; MutedCategories, MutedMerchants, JunkFingerprints map[string]bool; Recurrence int; LastRecurrence time.Time }` (the two muted maps are keyed lowercase)
  - `type Result struct { Decision Decision; Score int; WatchlistHit bool; Reasons []string }` (the block reason, if any, is `Reasons[0]`)
  - `func New(r config.Rules, watchlist []string) *Engine`, `func (e *Engine) Window() time.Duration`, `func (e *Engine) Evaluate(d Deal, f Facts) Result`
  - `judge.Opinion`, `judge.Judge` interface, `judge.Noop`

- [ ] **Step 1: Write the failing tests**

`internal/rules/rules_test.go`:
```go
package rules

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
)

func intp(v int) *int { return &v }

func engine() *Engine {
	return New(config.Rules{
		MinTemperature:    100,
		CategoryOverrides: map[string]int{"Elektronik": 60},
		MutedCategories:   []string{"Fashion"},
		MutedMerchants:    []string{"Example Air"},
		BlockKeywords:     []string{"Gratisprobe"},
		Recurrence:        config.Recurrence{Window: config.Duration(90 * 24 * time.Hour), MaxSeen: 2},
	}, []string{"RTX 5080", "oled"})
}

func TestEvaluate(t *testing.T) {
	hot := Deal{Title: "Example Laptop", Merchant: "Shop", Category: "Computer", Fingerprint: "shop|laptop", Temperature: intp(150), InTrending: true}
	with := func(f func(*Deal)) Deal { d := hot; f(&d); return d }
	last := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		deal       Deal
		facts      Facts
		want       Decision
		wantReason string // prefix of Reasons[0]; "" means no reasons
		wantWatch  bool
		wantScore  int
	}{
		{"hot deal passes", hot, Facts{}, Pass, "", false, 150},
		{"too cold", with(func(d *Deal) { d.Temperature = intp(80) }), Facts{}, Block, "too cold: 80° < 100°", false, 80},
		{"category override", with(func(d *Deal) { d.Temperature = intp(80); d.Category = "Elektronik" }), Facts{}, Pass, "", false, 80},
		{"muted category from config, any case", with(func(d *Deal) { d.Category = "fashion" }), Facts{}, Block, "muted category: fashion", false, 150},
		{"muted category from a click", hot, Facts{MutedCategories: map[string]bool{"computer": true}}, Block, "muted category: Computer", false, 150},
		{"muted merchant", with(func(d *Deal) { d.Merchant = "EXAMPLE AIR" }), Facts{}, Block, "muted merchant: EXAMPLE AIR", false, 150},
		{"muted merchant from a click", hot, Facts{MutedMerchants: map[string]bool{"shop": true}}, Block, "muted merchant: Shop", false, 150},
		{"block keyword", with(func(d *Deal) { d.Title = "Gratisprobe Handcreme" }), Facts{}, Block, "block keyword: gratisprobe", false, 150},
		{"recurring", hot, Facts{Recurrence: 2, LastRecurrence: last}, Block, "recurring: seen 2× before in 90 days (last 2026-10-12)", false, 150},
		{"below recurrence limit", hot, Facts{Recurrence: 1, LastRecurrence: last}, Pass, "", false, 150},
		{"like your junk", hot, Facts{JunkFingerprints: map[string]bool{"shop|laptop": true}}, Block, "like a deal you marked as junk", false, 150},
		{"hidden beats watchlist", with(func(d *Deal) { d.Title = "Example RTX 5080" }), Facts{Hidden: true}, Block, "hidden by you", true, 1150},
		{"watchlist skips temperature", with(func(d *Deal) { d.Title = "Example rtx 5080 Karte"; d.Temperature = intp(5) }), Facts{}, Pass, "watchlist: rtx 5080", true, 1005},
		{"watchlist still blocked by keyword", with(func(d *Deal) { d.Title = "Gratisprobe OLED Reiniger" }), Facts{}, Block, "block keyword: gratisprobe", true, 1150},
		{"new-only without watchlist", with(func(d *Deal) { d.Temperature = nil; d.InTrending = false }), Facts{}, Block, "new-only", false, 0},
		{"new-only watchlist hit", with(func(d *Deal) { d.Title = "Example OLED TV"; d.Temperature = nil; d.InTrending = false }), Facts{}, Pass, "watchlist: oled", true, 1000},
		{"unknown temperature in trending", with(func(d *Deal) { d.Temperature = nil }), Facts{}, Pass, "", false, 0},
	}
	e := engine()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.Evaluate(tt.deal, tt.facts)
			if got.Decision != tt.want || got.WatchlistHit != tt.wantWatch || got.Score != tt.wantScore {
				t.Errorf("got %+v, want decision %s, watchlist %v, score %d", got, tt.want, tt.wantWatch, tt.wantScore)
			}
			if tt.wantReason == "" {
				if len(got.Reasons) != 0 {
					t.Errorf("reasons = %v, want none", got.Reasons)
				}
			} else if len(got.Reasons) == 0 || !strings.HasPrefix(got.Reasons[0], tt.wantReason) {
				t.Errorf("reasons = %v, want first to start with %q", got.Reasons, tt.wantReason)
			}
		})
	}
}

func TestBlockedWatchlistHitKeepsBothReasons(t *testing.T) {
	got := engine().Evaluate(Deal{Title: "Example RTX 5080", Temperature: intp(150), InTrending: true}, Facts{Hidden: true})
	if !slices.Equal(got.Reasons, []string{"hidden by you", "watchlist: rtx 5080"}) {
		t.Errorf("reasons = %v", got.Reasons)
	}
}

func TestWindow(t *testing.T) {
	if engine().Window() != 90*24*time.Hour {
		t.Errorf("window = %v", engine().Window())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/rules/`
Expected: FAIL (`undefined: New`).

- [ ] **Step 3: Implement the engine**

`internal/rules/rules.go`:
```go
// Package rules decides whether a deal passes, and records why.
package rules

import (
	"fmt"
	"strings"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
)

type Decision string

const (
	Pass  Decision = "pass"
	Block Decision = "block"
)

// watchlistBoost sorts watchlist hits above everything else.
const watchlistBoost = 1000

type Deal struct {
	Title       string
	Merchant    string
	Category    string
	Fingerprint string
	Temperature *int
	InTrending  bool
}

// Facts are what the store and the user's clicks know about a deal.
type Facts struct {
	Hidden           bool
	MutedCategories  map[string]bool // lowercase
	MutedMerchants   map[string]bool // lowercase
	JunkFingerprints map[string]bool
	Recurrence       int // other deals with the same fingerprint in the window
	LastRecurrence   time.Time
}

type Result struct {
	Decision     Decision
	Score        int
	WatchlistHit bool
	Reasons      []string // the block reason, if any, comes first
}

type Engine struct {
	minTemperature  int
	overrides       map[string]int
	mutedCategories map[string]bool
	mutedMerchants  map[string]bool
	blockKeywords   []string
	watchlist       []string
	window          time.Duration
	maxSeen         int
}

func New(r config.Rules, watchlist []string) *Engine {
	e := &Engine{
		minTemperature:  r.MinTemperature,
		overrides:       make(map[string]int, len(r.CategoryOverrides)),
		mutedCategories: lowerSet(r.MutedCategories),
		mutedMerchants:  lowerSet(r.MutedMerchants),
		blockKeywords:   lowerAll(r.BlockKeywords),
		watchlist:       lowerAll(watchlist),
		window:          r.Recurrence.Window.D(),
		maxSeen:         r.Recurrence.MaxSeen,
	}
	for k, v := range r.CategoryOverrides {
		e.overrides[strings.ToLower(k)] = v
	}
	return e
}

func (e *Engine) Window() time.Duration { return e.window }

func (e *Engine) Evaluate(d Deal, f Facts) Result {
	// Keywords match the full title: cleaning would remove numbers like "5080".
	title := strings.ToLower(d.Title)
	res := Result{Decision: Pass}
	if d.Temperature != nil {
		res.Score = *d.Temperature
	}
	if kw, ok := firstContained(title, e.watchlist); ok {
		res.WatchlistHit = true
		res.Score += watchlistBoost
		res.Reasons = append(res.Reasons, "watchlist: "+kw)
	}
	if reason, blocked := e.blockReason(d, f, title, res.WatchlistHit); blocked {
		res.Decision = Block
		res.Reasons = append([]string{reason}, res.Reasons...)
	}
	return res
}

func (e *Engine) blockReason(d Deal, f Facts, title string, watchlistHit bool) (string, bool) {
	category, merchant := strings.ToLower(d.Category), strings.ToLower(d.Merchant)
	switch {
	case f.Hidden:
		return "hidden by you", true
	case category != "" && (e.mutedCategories[category] || f.MutedCategories[category]):
		return "muted category: " + d.Category, true
	case merchant != "" && (e.mutedMerchants[merchant] || f.MutedMerchants[merchant]):
		return "muted merchant: " + d.Merchant, true
	}
	if kw, ok := firstContained(title, e.blockKeywords); ok {
		return "block keyword: " + kw, true
	}
	if f.Recurrence >= e.maxSeen {
		return fmt.Sprintf("recurring: seen %d× before in %d days (last %s)",
			f.Recurrence, int(e.window.Hours()/24), f.LastRecurrence.Format("2006-01-02")), true
	}
	if f.JunkFingerprints[d.Fingerprint] {
		return "like a deal you marked as junk", true
	}
	if watchlistHit {
		return "", false
	}
	if d.Temperature != nil {
		if minimum := e.minTemperatureFor(category); *d.Temperature < minimum {
			return fmt.Sprintf("too cold: %d° < %d°", *d.Temperature, minimum), true
		}
	}
	if !d.InTrending {
		return "new-only", true
	}
	return "", false
}

func (e *Engine) minTemperatureFor(category string) int {
	if v, ok := e.overrides[category]; ok {
		return v
	}
	return e.minTemperature
}

func firstContained(title string, keywords []string) (string, bool) {
	for _, kw := range keywords {
		if kw != "" && strings.Contains(title, kw) {
			return kw, true
		}
	}
	return "", false
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
}

func lowerSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range lowerAll(in) {
		out[s] = true
	}
	return out
}
```

- [ ] **Step 4: Add the judge interface**

`internal/judge/judge.go`:
```go
// Package judge is where an LLM's opinion on a deal will plug in. v1 ships
// only Noop; see the spec's "Later" section.
package judge

import (
	"context"

	"github.com/MorrisMorrison/dealsieve/internal/rules"
)

type Opinion struct {
	Block  bool
	Reason string
}

type Judge interface {
	Judge(ctx context.Context, d rules.Deal) (Opinion, error)
}

type Noop struct{}

func (Noop) Judge(context.Context, rules.Deal) (Opinion, error) { return Opinion{}, nil }
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/rules/ ./internal/judge/`
Expected: `ok` for rules, `[no test files]` for judge.

- [ ] **Step 6: Commit**

```bash
git add internal/rules internal/judge
git commit -m "feat(rules): rule engine with reasons, plus a no-op judge interface" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Notifiers

**Files:**
- Create: `internal/notify/notify.go`, `internal/notify/ntfy.go`, `internal/notify/telegram.go`
- Test: `internal/notify/notify_test.go`

**Interfaces:**
- Consumes: `config.Channel`, `config.Env` (Task 1).
- Produces:
  - `type Message struct { Title, Body, ClickURL, ImageURL string }`
  - `type Notifier interface { Send(ctx context.Context, m Message) error }`
  - `func DealMessage(title, price, merchant string, temperature *int, reasons []string, url, imageURL string) Message`
  - `func FromConfig(channels map[string]config.Channel, env config.Env, hc *http.Client) map[string]Notifier`
  - `type Ntfy struct { BaseURL, Topic, Priority, Token string; HTTP *http.Client }`
  - `type Telegram struct { APIBase, Token, ChatID string; HTTP *http.Client }`

- [ ] **Step 1: Write the failing tests**

`internal/notify/notify_test.go`:
```go
package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MorrisMorrison/dealsieve/internal/config"
)

var ctx = context.Background()

func intp(v int) *int { return &v }

func TestDealMessage(t *testing.T) {
	m := DealMessage("Example OLED TV", "899€", "Example Shop", intp(142), []string{"watchlist: oled"}, "https://d.example.test/1", "https://i.example.test/1.jpg")
	want := Message{Title: "Example OLED TV", Body: "899€ · 142° · Example Shop\nwatchlist: oled", ClickURL: "https://d.example.test/1", ImageURL: "https://i.example.test/1.jpg"}
	if m != want {
		t.Errorf("got %+v, want %+v", m, want)
	}
	if m := DealMessage("T", "", "", nil, nil, "u", ""); m.Body != "" {
		t.Errorf("empty body = %q", m.Body)
	}
}

func TestNtfySend(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	n := &Ntfy{BaseURL: srv.URL, Topic: "deals", Priority: "high", Token: "tk", HTTP: srv.Client()}
	if err := n.Send(ctx, Message{Title: "Döner", Body: "b", ClickURL: "https://d.example.test", ImageURL: "https://i.example.test/x.jpg"}); err != nil {
		t.Fatal(err)
	}
	if path != "/" || auth != "Bearer tk" {
		t.Errorf("path %q, auth %q", path, auth)
	}
	if got["topic"] != "deals" || got["title"] != "Döner" || got["message"] != "b" || got["priority"] != float64(4) ||
		got["click"] != "https://d.example.test" || got["attach"] != "https://i.example.test/x.jpg" {
		t.Errorf("payload = %v", got)
	}
}

func TestNtfyOmitsEmptyAttachAndAuth(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	if err := (&Ntfy{BaseURL: srv.URL, Topic: "deals", HTTP: srv.Client()}).Send(ctx, Message{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["attach"]; ok || auth != "" || got["priority"] != float64(3) {
		t.Errorf("payload = %v, auth %q", got, auth)
	}
}

func TestNtfyErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := (&Ntfy{BaseURL: srv.URL, Topic: "t", HTTP: srv.Client()}).Send(ctx, Message{}); err == nil {
		t.Fatal("want an error")
	}
}

type telegramCall struct {
	path    string
	payload map[string]string
}

func telegramServer(t *testing.T, failPhoto bool) (*httptest.Server, *[]telegramCall) {
	t.Helper()
	var calls []telegramCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]string
		json.NewDecoder(r.Body).Decode(&p)
		calls = append(calls, telegramCall{r.URL.Path, p})
		if failPhoto && strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			http.Error(w, `{"ok":false}`, http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestTelegramSendsPhoto(t *testing.T) {
	srv, calls := telegramServer(t, false)
	tg := &Telegram{APIBase: srv.URL, Token: "TOKEN", ChatID: "42", HTTP: srv.Client()}
	if err := tg.Send(ctx, Message{Title: "Deal", Body: "9€", ClickURL: "https://d.example.test", ImageURL: "https://i.example.test/x.jpg"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %v", *calls)
	}
	c := (*calls)[0]
	if c.path != "/botTOKEN/sendPhoto" || c.payload["chat_id"] != "42" || c.payload["photo"] != "https://i.example.test/x.jpg" ||
		c.payload["caption"] != "Deal\n9€\nhttps://d.example.test" {
		t.Errorf("call = %+v", c)
	}
}

func TestTelegramFallsBackToText(t *testing.T) {
	srv, calls := telegramServer(t, true)
	tg := &Telegram{APIBase: srv.URL, Token: "TOKEN", ChatID: "42", HTTP: srv.Client()}
	if err := tg.Send(ctx, Message{Title: "Deal", ImageURL: "https://i.example.test/x.jpg"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[1].path != "/botTOKEN/sendMessage" || (*calls)[1].payload["text"] != "Deal\n" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestTelegramErrorHidesToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // connection refused
	tg := &Telegram{APIBase: srv.URL, Token: "SECRET123", ChatID: "42", HTTP: http.DefaultClient}
	err := tg.Send(ctx, Message{Title: "x"})
	if err == nil || strings.Contains(err.Error(), "SECRET123") {
		t.Fatalf("err = %v; want an error without the token", err)
	}
}

func TestFromConfig(t *testing.T) {
	got := FromConfig(map[string]config.Channel{
		"a": {Type: "ntfy", URL: "https://n.example.com", Topic: "t", Priority: "low"},
		"b": {Type: "telegram", ChatID: "1"},
	}, config.Env{TelegramToken: "tok", NtfyToken: "nt"}, http.DefaultClient)
	if n, ok := got["a"].(*Ntfy); !ok || n.Token != "nt" || n.Priority != "low" {
		t.Errorf("a = %#v", got["a"])
	}
	if tg, ok := got["b"].(*Telegram); !ok || tg.Token != "tok" || tg.APIBase != "https://api.telegram.org" {
		t.Errorf("b = %#v", got["b"])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/notify/`
Expected: FAIL (`undefined: DealMessage`, …).

- [ ] **Step 3: Implement notify.go**

`internal/notify/notify.go`:
```go
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
		body += "\n" + strings.Join(reasons, ", ")
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
```

- [ ] **Step 4: Implement ntfy.go**

`internal/notify/ntfy.go`:
```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var ntfyPriority = map[string]int{"min": 1, "low": 2, "": 3, "default": 3, "high": 4, "urgent": 5}

type Ntfy struct {
	BaseURL  string
	Topic    string
	Priority string
	Token    string
	HTTP     *http.Client
}

// Send uses ntfy's JSON publishing, which keeps non-ASCII titles intact.
func (n *Ntfy) Send(ctx context.Context, m Message) error {
	payload := struct {
		Topic    string `json:"topic"`
		Title    string `json:"title,omitempty"`
		Message  string `json:"message,omitempty"`
		Priority int    `json:"priority"`
		Click    string `json:"click,omitempty"`
		Attach   string `json:"attach,omitempty"`
	}{n.Topic, m.Title, m.Body, ntfyPriority[n.Priority], m.ClickURL, m.ImageURL}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.BaseURL, "/")+"/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy: status %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}
```

- [ ] **Step 5: Implement telegram.go**

`internal/notify/telegram.go`:
```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
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
	text := m.Title + "\n" + m.Body
	if m.ClickURL != "" {
		text += "\n" + m.ClickURL
	}
	if m.ImageURL != "" {
		err := t.call(ctx, "sendPhoto", map[string]string{"chat_id": t.ChatID, "photo": m.ImageURL, "caption": truncate(text, 1024)})
		if err == nil {
			return nil
		}
		// Telegram can't always fetch the image; the text alone still helps.
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
		return fmt.Errorf("telegram %s: status %d: %s", method, resp.StatusCode, bytes.TrimSpace(msg))
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
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/notify/`
Expected: `ok`

- [ ] **Step 7: Commit**

```bash
git add internal/notify
git commit -m "feat(notify): ntfy and Telegram notifiers" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Pipeline (ingest → verdict → notify)

**Files:**
- Create: `internal/pipeline/pipeline.go`
- Test: `internal/pipeline/pipeline_test.go`

**Interfaces:**
- Consumes: `store.*` (Task 4), `rules.Engine`, `rules.Deal`, `rules.Facts`, `rules.Pass` (Task 5), `notify.Notifier`, `notify.DealMessage` (Task 6), `fingerprint.CleanTitle`, `fingerprint.Key` (Task 3), `config.NotifyRule` (Task 1), `source.Deal`, `source.Feed` (Task 2).
- Produces:
  - `type Pipeline struct { Store *store.Store; Engine *rules.Engine; Notify []config.NotifyRule; Notifiers map[string]notify.Notifier; Now func() time.Time; Log *slog.Logger }`
  - `func (p *Pipeline) Ingest(ctx context.Context, feed source.Feed, deals []source.Deal) error`
  - `func (p *Pipeline) RecomputeRecent(ctx context.Context) error`
  - `func (p *Pipeline) Prune(ctx context.Context) (int64, error)`

- [ ] **Step 1: Write the failing tests**

`internal/pipeline/pipeline_test.go`:
```go
package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/fingerprint"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/rules"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

var ctx = context.Background()

type fakeNotifier struct {
	mu   sync.Mutex
	sent []notify.Message
	fail bool
}

func (f *fakeNotifier) Send(_ context.Context, m notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("boom")
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

var baseRules = config.Rules{
	MinTemperature: 100,
	BlockKeywords:  []string{"gratisprobe"},
	Recurrence:     config.Recurrence{Window: config.Duration(90 * 24 * time.Hour), MaxSeen: 2},
}

type harness struct {
	p    *Pipeline
	st   *store.Store
	now  *time.Time
	push *fakeNotifier
}

func newHarness(t *testing.T, when config.When) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	push := &fakeNotifier{}
	p := &Pipeline{
		Store:     st,
		Engine:    rules.New(baseRules, []string{"oled"}),
		Notify:    []config.NotifyRule{{When: when, Channels: []string{"push"}}},
		Notifiers: map[string]notify.Notifier{"push": push},
		Now:       func() time.Time { return now },
		Log:       slog.New(slog.DiscardHandler),
	}
	return &harness{p: p, st: st, now: &now, push: push}
}

func intp(v int) *int { return &v }

func deal(id, title string, temp *int) source.Deal {
	return source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
		Title: title, Merchant: "Example Shop", Price: "9€", Category: "Elektronik", Temperature: temp}
}

func (h *harness) ingest(t *testing.T, feed source.Feed, deals ...source.Deal) {
	t.Helper()
	if err := h.p.Ingest(ctx, feed, deals); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) verdict(t *testing.T, id string) store.Verdict {
	t.Helper()
	v, err := h.st.GetVerdict(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWatchlistDealIsNotifiedOnce(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	d := deal("1", "Example OLED TV", intp(150))
	h.ingest(t, source.FeedTrending, d)
	h.ingest(t, source.FeedTrending, d)

	if v := h.verdict(t, "mydealz:1"); v.Decision != "pass" || !v.WatchlistHit {
		t.Errorf("verdict = %+v", v)
	}
	if h.push.count() != 1 {
		t.Fatalf("sent %d notifications, want 1", h.push.count())
	}
	if m := h.push.sent[0]; m.Title != "Example OLED TV" || !strings.Contains(m.Body, "watchlist: oled") {
		t.Errorf("message = %+v", m)
	}
}

func TestNewFeedWatchlistHitIsNotified(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedNew, deal("1", "Example OLED TV", nil), deal("2", "Example Kabel", nil))
	if h.push.count() != 1 {
		t.Errorf("sent %d, want 1", h.push.count())
	}
	if v := h.verdict(t, "mydealz:2"); v.Decision != "block" {
		t.Errorf("non-watchlist new-only deal = %+v, want block", v)
	}
}

func TestBlockedDealIsNotNotified(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Gratisprobe OLED Reiniger", intp(150)))
	if h.push.count() != 0 {
		t.Errorf("sent %d, want 0", h.push.count())
	}
}

func TestFailedNotificationIsRetriedThreeTimesAtMost(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.push.fail = true
	h.ingest(t, source.FeedTrending, deal("1", "Example OLED TV", intp(150))) // attempt 1
	h.ingest(t, source.FeedTrending)                                          // attempt 2 (retry)
	h.ingest(t, source.FeedTrending)                                          // attempt 3 (retry)
	h.push.fail = false
	h.ingest(t, source.FeedTrending) // no attempts left

	sent, attempts, err := h.st.NotificationState(ctx, "mydealz:1", "push")
	if err != nil || sent || attempts != 3 || h.push.count() != 0 {
		t.Errorf("sent %v, attempts %d, delivered %d, err %v", sent, attempts, h.push.count(), err)
	}
}

func TestFailedNotificationSucceedsOnRetry(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.push.fail = true
	h.ingest(t, source.FeedTrending, deal("1", "Example OLED TV", intp(150)))
	h.push.fail = false
	h.ingest(t, source.FeedTrending)
	if sent, _, _ := h.st.NotificationState(ctx, "mydealz:1", "push"); !sent || h.push.count() != 1 {
		t.Errorf("sent %v, delivered %d", sent, h.push.count())
	}
}

func TestTemperatureNotifyFiresWhenDealHeatsUp(t *testing.T) {
	h := newHarness(t, config.When{MinTemperature: 500})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(300)))
	if h.push.count() != 0 {
		t.Fatalf("sent %d at 300°", h.push.count())
	}
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(520)))
	if h.push.count() != 1 {
		t.Errorf("sent %d at 520°, want 1", h.push.count())
	}
}

func TestRecurringDealIsBlocked(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	title := "Example Air Flash Sale: Flüge ab 19,99€"
	for i, id := range []string{"1", "2", "3"} {
		*h.now = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * 20 * 24 * time.Hour)
		h.ingest(t, source.FeedTrending, deal(id, title, intp(150)))
	}
	if v := h.verdict(t, "mydealz:2"); v.Decision != "pass" {
		t.Errorf("second occurrence = %+v, want pass", v)
	}
	if v := h.verdict(t, "mydealz:3"); v.Decision != "block" || !strings.HasPrefix(v.Reasons[0], "recurring: seen 2× before") {
		t.Errorf("third occurrence = %+v, want recurring block", v)
	}
}

func TestRecomputeAppliesActions(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(150)), deal("2", "Example Monitor", intp(150)))

	if _, err := h.st.AddAction(ctx, store.ActionHide, "mydealz:1", *h.now); err != nil {
		t.Fatal(err)
	}
	fp := fingerprint.Key("Example Shop", fingerprint.CleanTitle("Example Monitor"))
	if _, err := h.st.AddAction(ctx, store.ActionJunk, fp, *h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.p.RecomputeRecent(ctx); err != nil {
		t.Fatal(err)
	}
	if v := h.verdict(t, "mydealz:1"); v.Decision != "block" || v.Reasons[0] != "hidden by you" {
		t.Errorf("hidden deal = %+v", v)
	}
	if v := h.verdict(t, "mydealz:2"); v.Decision != "block" || v.Reasons[0] != "like a deal you marked as junk" {
		t.Errorf("junk deal = %+v", v)
	}
}

func TestMuteCategoryActionIsCaseInsensitive(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(150)))
	if _, err := h.st.AddAction(ctx, store.ActionMuteCategory, "ELEKTRONIK", *h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.p.RecomputeRecent(ctx); err != nil {
		t.Fatal(err)
	}
	if v := h.verdict(t, "mydealz:1"); v.Decision != "block" {
		t.Errorf("verdict = %+v, want block", v)
	}
}

func TestPrune(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(150)))
	*h.now = h.now.Add(181 * 24 * time.Hour)
	if n, err := h.p.Prune(ctx); err != nil || n != 1 {
		t.Errorf("pruned %d, %v; want 1", n, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/pipeline/`
Expected: FAIL (`undefined: Pipeline`).

- [ ] **Step 3: Implement**

`internal/pipeline/pipeline.go`:
```go
// Package pipeline runs a polled feed through storage, the rules and the
// notifiers, and recomputes verdicts when the user's actions change.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/fingerprint"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/rules"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

const (
	recomputeWindow   = 7 * 24 * time.Hour
	retention         = 180 * 24 * time.Hour
	maxNotifyAttempts = 3
)

type Pipeline struct {
	Store     *store.Store
	Engine    *rules.Engine
	Notify    []config.NotifyRule
	Notifiers map[string]notify.Notifier
	Now       func() time.Time
	Log       *slog.Logger
}

// Ingest stores what a feed returned, re-evaluates those deals and sends
// notifications. Failed notifications from earlier polls are retried first.
func (p *Pipeline) Ingest(ctx context.Context, feed source.Feed, deals []source.Deal) error {
	now := p.Now()
	p.retryPending(ctx, now)
	facts, err := p.actionFacts(ctx)
	if err != nil {
		return err
	}
	for _, d := range deals {
		clean := fingerprint.CleanTitle(d.Title)
		rec, err := p.Store.UpsertDeal(ctx, d, feed, clean, fingerprint.Key(d.Merchant, clean), now)
		if err != nil {
			return fmt.Errorf("store deal %s: %w", d.ID, err)
		}
		res, err := p.decide(ctx, rec, facts, now)
		if err != nil {
			return err
		}
		if res.Decision == rules.Pass {
			for _, ch := range p.channelsFor(rec, res) {
				p.send(ctx, rec, res.Reasons, ch, now)
			}
		}
	}
	return nil
}

// RecomputeRecent re-evaluates recent deals, e.g. after an action. It never
// notifies: undoing a mute shouldn't ping about old deals.
func (p *Pipeline) RecomputeRecent(ctx context.Context) error {
	now := p.Now()
	facts, err := p.actionFacts(ctx)
	if err != nil {
		return err
	}
	recs, err := p.Store.DealsSeenSince(ctx, now.Add(-recomputeWindow))
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if _, err := p.decide(ctx, rec, facts, now); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) Prune(ctx context.Context) (int64, error) {
	return p.Store.Prune(ctx, p.Now().Add(-retention))
}

type actionFacts struct {
	hidden, mutedCategories, mutedMerchants, junk map[string]bool
}

func (p *Pipeline) actionFacts(ctx context.Context) (actionFacts, error) {
	actions, err := p.Store.Actions(ctx)
	if err != nil {
		return actionFacts{}, err
	}
	f := actionFacts{hidden: map[string]bool{}, mutedCategories: map[string]bool{}, mutedMerchants: map[string]bool{}, junk: map[string]bool{}}
	for _, a := range actions {
		switch a.Kind {
		case store.ActionHide:
			f.hidden[a.Value] = true
		case store.ActionJunk:
			f.junk[a.Value] = true
		case store.ActionMuteCategory:
			f.mutedCategories[strings.ToLower(a.Value)] = true
		case store.ActionMuteMerchant:
			f.mutedMerchants[strings.ToLower(a.Value)] = true
		}
	}
	return f, nil
}

func (p *Pipeline) decide(ctx context.Context, rec store.DealRecord, af actionFacts, now time.Time) (rules.Result, error) {
	n, last, err := p.Store.CountRecurrence(ctx, rec.Fingerprint, rec.ID, now.Add(-p.Engine.Window()))
	if err != nil {
		return rules.Result{}, err
	}
	res := p.Engine.Evaluate(rules.Deal{
		Title: rec.Title, Merchant: rec.Merchant, Category: rec.Category, Fingerprint: rec.Fingerprint,
		Temperature: rec.Temperature, InTrending: rec.InTrending,
	}, rules.Facts{
		Hidden: af.hidden[rec.ID], MutedCategories: af.mutedCategories, MutedMerchants: af.mutedMerchants,
		JunkFingerprints: af.junk, Recurrence: n, LastRecurrence: last,
	})
	err = p.Store.SaveVerdict(ctx, store.Verdict{
		DealID: rec.ID, Decision: string(res.Decision), Score: res.Score,
		WatchlistHit: res.WatchlistHit, Reasons: res.Reasons, DecidedAt: now,
	})
	return res, err
}

func (p *Pipeline) channelsFor(rec store.DealRecord, res rules.Result) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range p.Notify {
		hot := n.When.MinTemperature > 0 && rec.Temperature != nil && *rec.Temperature >= n.When.MinTemperature
		if !(n.When.Watchlist && res.WatchlistHit) && !hot {
			continue
		}
		for _, ch := range n.Channels {
			if !seen[ch] {
				seen[ch] = true
				out = append(out, ch)
			}
		}
	}
	return out
}

func (p *Pipeline) send(ctx context.Context, rec store.DealRecord, reasons []string, channel string, now time.Time) {
	sent, attempts, err := p.Store.NotificationState(ctx, rec.ID, channel)
	if err != nil {
		p.Log.Error("reading notification state", "deal", rec.ID, "channel", channel, "err", err)
		return
	}
	if sent || attempts >= maxNotifyAttempts {
		return
	}
	n, ok := p.Notifiers[channel]
	if !ok {
		p.Log.Error("no notifier for channel", "channel", channel)
		return
	}
	sendErr := n.Send(ctx, notify.DealMessage(rec.Title, rec.Price, rec.Merchant, rec.Temperature, reasons, rec.URL, rec.ImageURL))
	if sendErr != nil {
		p.Log.Warn("notification failed", "deal", rec.ID, "channel", channel, "attempt", attempts+1, "err", sendErr)
	}
	if err := p.Store.RecordNotification(ctx, rec.ID, channel, sendErr == nil, now); err != nil {
		p.Log.Error("recording notification", "deal", rec.ID, "channel", channel, "err", err)
	}
}

func (p *Pipeline) retryPending(ctx context.Context, now time.Time) {
	pending, err := p.Store.PendingNotifications(ctx, maxNotifyAttempts)
	if err != nil {
		p.Log.Error("listing pending notifications", "err", err)
		return
	}
	for _, pn := range pending {
		rec, err := p.Store.GetDeal(ctx, pn.DealID)
		if err != nil {
			continue
		}
		v, err := p.Store.GetVerdict(ctx, pn.DealID)
		if err != nil || v.Decision != string(rules.Pass) {
			continue
		}
		p.send(ctx, rec, v.Reasons, pn.Channel, now)
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/pipeline/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline
git commit -m "feat(pipeline): ingest deals, decide verdicts, notify with retries" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Poller

**Files:**
- Create: `internal/poller/poller.go`
- Test: `internal/poller/poller_test.go`

**Interfaces:**
- Consumes: `source.Fetcher`, `source.Deal`, `source.Feed` (Task 2).
- Produces:
  - `type Ingester interface { Ingest(ctx context.Context, feed source.Feed, deals []source.Deal) error }` (`*pipeline.Pipeline` satisfies it)
  - `type Status struct { Feed, Interval string; LastSuccess, LastAttempt *time.Time; LastError string; Failures int; Stale bool }` (JSON: `feed`, `interval`, `last_success`, `last_attempt`, `last_error`, `consecutive_failures`, `stale`)
  - `func New(f source.Fetcher, ing Ingester, intervals map[source.Feed]time.Duration, log *slog.Logger) *Poller`
  - `func (p *Poller) Run(ctx context.Context)` (blocks until ctx is done), `func (p *Poller) PollOnce(ctx context.Context, feed source.Feed) int`, `func (p *Poller) Status() []Status`

- [ ] **Step 1: Write the failing tests**

`internal/poller/poller_test.go`:
```go
package poller

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

type fakeFetcher struct {
	deals []source.Deal
	err   error
}

func (f *fakeFetcher) Fetch(context.Context, source.Feed) ([]source.Deal, error) { return f.deals, f.err }

type fakeIngester struct {
	calls int
	err   error
}

func (f *fakeIngester) Ingest(context.Context, source.Feed, []source.Deal) error {
	f.calls++
	return f.err
}

func TestNextDelay(t *testing.T) {
	tests := []struct {
		interval time.Duration
		failures int
		r        float64
		want     time.Duration
	}{
		{5 * time.Minute, 0, 0.5, 5 * time.Minute},
		{5 * time.Minute, 1, 0.5, 10 * time.Minute},
		{5 * time.Minute, 2, 0.5, 20 * time.Minute},
		{5 * time.Minute, 10, 0.5, 30 * time.Minute},
		{60 * time.Minute, 3, 0.5, 60 * time.Minute},
		{10 * time.Minute, 0, 0, 9 * time.Minute},
		{10 * time.Minute, 0, 1, 11 * time.Minute},
	}
	for _, tt := range tests {
		if got := nextDelay(tt.interval, tt.failures, tt.r); got != tt.want {
			t.Errorf("nextDelay(%v, %d, %v) = %v, want %v", tt.interval, tt.failures, tt.r, got, tt.want)
		}
	}
}

func newTestPoller(f source.Fetcher, ing Ingester, now *time.Time) *Poller {
	p := New(f, ing, map[source.Feed]time.Duration{source.FeedTrending: 5 * time.Minute, source.FeedNew: 2 * time.Minute}, slog.New(slog.DiscardHandler))
	p.now = func() time.Time { return *now }
	p.started = *now
	return p
}

func TestPollOnceTracksFailuresAndRecovery(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	f := &fakeFetcher{err: errors.New("unexpected status 503")}
	ing := &fakeIngester{}
	p := newTestPoller(f, ing, &now)

	if n := p.PollOnce(context.Background(), source.FeedTrending); n != 1 {
		t.Errorf("failures = %d, want 1", n)
	}
	if n := p.PollOnce(context.Background(), source.FeedTrending); n != 2 {
		t.Errorf("failures = %d, want 2", n)
	}
	if ing.calls != 0 {
		t.Errorf("ingest called %d times on fetch errors", ing.calls)
	}
	st := p.Status()[1] // sorted: new, trending
	if st.Feed != "trending" || st.LastError != "unexpected status 503" || st.Failures != 2 || st.LastSuccess != nil {
		t.Errorf("status = %+v", st)
	}

	f.err = nil
	if n := p.PollOnce(context.Background(), source.FeedTrending); n != 0 {
		t.Errorf("failures after success = %d", n)
	}
	st = p.Status()[1]
	if st.LastError != "" || st.LastSuccess == nil || !st.LastSuccess.Equal(now) || ing.calls != 1 {
		t.Errorf("status after success = %+v (ingest calls %d)", st, ing.calls)
	}
}

func TestIngestErrorCountsAsFailure(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	p := newTestPoller(&fakeFetcher{}, &fakeIngester{err: errors.New("disk full")}, &now)
	if n := p.PollOnce(context.Background(), source.FeedNew); n != 1 {
		t.Errorf("failures = %d, want 1", n)
	}
}

func TestStale(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	p := newTestPoller(&fakeFetcher{}, &fakeIngester{}, &now)

	now = now.Add(10 * time.Minute) // never polled: new (2m) is stale, trending (5m) is not yet
	st := p.Status()
	if !st[0].Stale || st[1].Stale {
		t.Errorf("before any poll: %+v", st)
	}
	p.PollOnce(context.Background(), source.FeedNew)
	now = now.Add(6 * time.Minute) // new: 6m since success (not > 6m); trending: 16m since start (> 15m)
	if st := p.Status(); st[0].Stale || !st[1].Stale {
		t.Errorf("after polling new: %+v", st)
	}
}

func TestRunStopsWithContext(t *testing.T) {
	p := New(&fakeFetcher{}, &fakeIngester{}, map[source.Feed]time.Duration{source.FeedNew: time.Minute}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't stop")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/poller/`
Expected: FAIL (`undefined: New`, `undefined: nextDelay`).

- [ ] **Step 3: Implement**

`internal/poller/poller.go`:
```go
// Package poller fetches each feed on its own interval, backs off on
// failures and keeps per-feed status for the page.
package poller

import (
	"context"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

const maxBackoff = 30 * time.Minute

type Ingester interface {
	Ingest(ctx context.Context, feed source.Feed, deals []source.Deal) error
}

type Status struct {
	Feed        string     `json:"feed"`
	Interval    string     `json:"interval"`
	LastSuccess *time.Time `json:"last_success"`
	LastAttempt *time.Time `json:"last_attempt"`
	LastError   string     `json:"last_error,omitempty"`
	Failures    int        `json:"consecutive_failures"`
	Stale       bool       `json:"stale"`
}

type feedState struct {
	lastSuccess time.Time
	lastAttempt time.Time
	lastErr     string
	failures    int
}

type Poller struct {
	fetcher   source.Fetcher
	ingest    Ingester
	intervals map[source.Feed]time.Duration
	log       *slog.Logger
	now       func() time.Time
	started   time.Time

	mu    sync.Mutex
	state map[source.Feed]*feedState
}

func New(f source.Fetcher, ing Ingester, intervals map[source.Feed]time.Duration, log *slog.Logger) *Poller {
	p := &Poller{fetcher: f, ingest: ing, intervals: intervals, log: log, now: time.Now, state: map[source.Feed]*feedState{}}
	p.started = p.now()
	for feed := range intervals {
		p.state[feed] = &feedState{}
	}
	return p
}

// Run polls every feed right away and then on its interval until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for feed, interval := range p.intervals {
		wg.Go(func() {
			for {
				failures := p.PollOnce(ctx, feed)
				timer := time.NewTimer(nextDelay(interval, failures, rand.Float64()))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		})
	}
	wg.Wait()
}

// PollOnce fetches and ingests one feed and returns the consecutive failures.
func (p *Poller) PollOnce(ctx context.Context, feed source.Feed) int {
	deals, err := p.fetcher.Fetch(ctx, feed)
	if err == nil {
		err = p.ingest.Ingest(ctx, feed, deals)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.state[feed]
	st.lastAttempt = p.now()
	if err != nil {
		st.failures++
		st.lastErr = err.Error()
		if ctx.Err() == nil {
			p.log.Warn("poll failed", "feed", feed, "failures", st.failures, "err", err)
		}
		return st.failures
	}
	st.failures, st.lastErr, st.lastSuccess = 0, "", st.lastAttempt
	p.log.Debug("polled", "feed", feed, "deals", len(deals))
	return 0
}

func (p *Poller) Status() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	out := make([]Status, 0, len(p.state))
	for feed, st := range p.state {
		interval := p.intervals[feed]
		ref := st.lastSuccess
		if ref.IsZero() {
			ref = p.started
		}
		s := Status{Feed: string(feed), Interval: interval.String(), LastError: st.lastErr, Failures: st.failures,
			Stale: now.Sub(ref) > 3*interval}
		if !st.lastSuccess.IsZero() {
			t := st.lastSuccess
			s.LastSuccess = &t
		}
		if !st.lastAttempt.IsZero() {
			t := st.lastAttempt
			s.LastAttempt = &t
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.Feed, b.Feed) })
	return out
}

// nextDelay doubles the interval per consecutive failure (capped at 30 min,
// or the interval if that's longer) and adds ±10% jitter; r is in [0, 1].
func nextDelay(interval time.Duration, failures int, r float64) time.Duration {
	limit := max(maxBackoff, interval)
	d := interval
	for range failures {
		if d >= limit {
			break
		}
		d *= 2
	}
	d = min(d, limit)
	return d + time.Duration(math.Round((r*0.2-0.1)*float64(d)))
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/poller/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/poller
git commit -m "feat(poller): per-feed polling with jitter, backoff and status" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: JSON API

**Files:**
- Create: `internal/api/api.go`
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: `store.*` (Task 4), `poller.Status` (Task 8), `config.Config` (Task 1).
- Produces:
  - `type Recomputer interface { RecomputeRecent(ctx context.Context) error }`
  - `type Server struct { Store *store.Store; Recompute Recomputer; Status func() []poller.Status; Config *config.Config; Assets fs.FS; Log *slog.Logger; Now func() time.Time }`
  - `func (s *Server) Handler() http.Handler`
  - Deal JSON fields (used by the page in Task 10): `id, url, title, merchant, price, category, image_url, temperature (number|null), temperatures (number[]), first_seen, last_seen, in_trending, in_new, decision, score, watchlist_hit, reasons (string[])`
  - Responses: `GET /api/deals` → `{"deals": [...]}`; `POST /api/actions` body `{"kind","deal_id"}` → `201` + action; `DELETE /api/actions/{id}` → `204`; `GET /api/rules` → `{"rules", "watchlist", "actions"}`; `GET /api/status` → `{"feeds": [...]}`; `GET /health` → `ok`; errors → `{"error": "..."}`.

- [ ] **Step 1: Write the failing tests**

`internal/api/api_test.go`:
```go
package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/poller"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

type fakeRecompute struct{ calls int }

func (f *fakeRecompute) RecomputeRecent(context.Context) error { f.calls++; return nil }

func intp(v int) *int { return &v }

type harness struct {
	srv *httptest.Server
	st  *store.Store
	rc  *fakeRecompute
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	seed := func(id, title, category string, temp int, at time.Time, decision string, reasons ...string) {
		d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
			Title: title, Merchant: "Example Shop", Price: "9€", Category: category, Temperature: intp(temp)}
		if _, err := st.UpsertDeal(ctx, d, source.FeedTrending, strings.ToLower(title), "shop|"+id, at); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveVerdict(ctx, store.Verdict{DealID: d.ID, Decision: decision, Score: temp, Reasons: reasons, DecidedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	seed("a", "Example OLED TV", "Elektronik", 150, now.Add(-time.Hour), "pass")
	seed("b", "Example Kabel", "", 80, now.Add(-time.Hour), "block", "too cold: 80° < 100°")
	seed("old", "Example Old", "Elektronik", 500, now.Add(-5*24*time.Hour), "pass")

	rc := &fakeRecompute{}
	s := &Server{
		Store:     st,
		Recompute: rc,
		Status:    func() []poller.Status { return []poller.Status{{Feed: "trending", Interval: "5m0s"}} },
		Config:    &config.Config{Watchlist: []string{"oled"}, Rules: config.Rules{MinTemperature: 100}},
		Assets:    fstest.MapFS{"index.html": {Data: []byte("<h1>dealsieve</h1>")}},
		Log:       slog.New(slog.DiscardHandler),
		Now:       func() time.Time { return now },
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &harness{srv: srv, st: st, rc: rc}
}

func (h *harness) do(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

type dealsResponse struct {
	Deals []struct {
		ID           string   `json:"id"`
		Title        string   `json:"title"`
		Temperature  *int     `json:"temperature"`
		Temperatures []int    `json:"temperatures"`
		Decision     string   `json:"decision"`
		Reasons      []string `json:"reasons"`
		InTrending   bool     `json:"in_trending"`
	} `json:"deals"`
}

func TestListDeals(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/api/deals", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got dealsResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Deals) != 1 {
		t.Fatalf("deals = %+v, want only the recent pass", got.Deals)
	}
	d := got.Deals[0]
	if d.ID != "mydealz:a" || *d.Temperature != 150 || !slices.Equal(d.Temperatures, []int{150}) ||
		d.Decision != "pass" || d.Reasons == nil || !d.InTrending {
		t.Errorf("deal = %+v", d)
	}

	_, body = h.do(t, "GET", "/api/deals?view=blocked", "")
	json.Unmarshal(body, &got)
	if len(got.Deals) != 1 || got.Deals[0].Reasons[0] != "too cold: 80° < 100°" {
		t.Errorf("blocked = %+v", got.Deals)
	}
	if resp, _ := h.do(t, "GET", "/api/deals?view=nope", ""); resp.StatusCode != 400 {
		t.Errorf("unknown view status = %d", resp.StatusCode)
	}
}

func TestAddAndDeleteAction(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "POST", "/api/actions", `{"kind":"mute_category","deal_id":"mydealz:a"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var a store.Action
	json.Unmarshal(body, &a)
	if a.Kind != store.ActionMuteCategory || a.Value != "Elektronik" || a.ID == 0 || h.rc.calls != 1 {
		t.Errorf("action %+v, recompute calls %d", a, h.rc.calls)
	}

	if resp, _ := h.do(t, "POST", "/api/actions", `{"kind":"junk","deal_id":"mydealz:a"}`); resp.StatusCode != 201 {
		t.Errorf("junk status = %d", resp.StatusCode)
	}
	actions, _ := h.st.Actions(context.Background())
	if actions[0].Value != "shop|a" {
		t.Errorf("junk value = %q, want the fingerprint", actions[0].Value)
	}

	if resp, _ := h.do(t, "DELETE", "/api/actions/"+jsonID(a.ID), ""); resp.StatusCode != 204 {
		t.Errorf("delete status = %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, "DELETE", "/api/actions/"+jsonID(a.ID), ""); resp.StatusCode != 404 {
		t.Errorf("second delete status = %d", resp.StatusCode)
	}
	if h.rc.calls != 3 {
		t.Errorf("recompute calls = %d, want 3", h.rc.calls)
	}
}

func jsonID(id int64) string { b, _ := json.Marshal(id); return string(b) }

func TestActionErrors(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		body string
		want int
	}{
		{`not json`, 400},
		{`{"kind":"explode","deal_id":"mydealz:a"}`, 400},
		{`{"kind":"hide_deal","deal_id":"mydealz:404"}`, 404},
		{`{"kind":"mute_category","deal_id":"mydealz:b"}`, 400}, // deal b has no category
	}
	for _, tt := range tests {
		if resp, body := h.do(t, "POST", "/api/actions", tt.body); resp.StatusCode != tt.want || !strings.Contains(string(body), `"error"`) {
			t.Errorf("%s: status %d (%s), want %d", tt.body, resp.StatusCode, body, tt.want)
		}
	}
	if resp, _ := h.do(t, "DELETE", "/api/actions/abc", ""); resp.StatusCode != 400 {
		t.Errorf("bad id status = %d", resp.StatusCode)
	}
	if h.rc.calls != 0 {
		t.Errorf("recompute ran %d times on errors", h.rc.calls)
	}
}

func TestRulesStatusHealthAndPage(t *testing.T) {
	h := newHarness(t)
	h.do(t, "POST", "/api/actions", `{"kind":"hide_deal","deal_id":"mydealz:a"}`)

	_, body := h.do(t, "GET", "/api/rules", "")
	var rules struct {
		Rules     map[string]any `json:"rules"`
		Watchlist []string       `json:"watchlist"`
		Actions   []store.Action `json:"actions"`
	}
	if err := json.Unmarshal(body, &rules); err != nil {
		t.Fatal(err)
	}
	if rules.Rules["min_temperature"] != float64(100) || rules.Watchlist[0] != "oled" || len(rules.Actions) != 1 {
		t.Errorf("rules = %s", body)
	}

	_, body = h.do(t, "GET", "/api/status", "")
	if !strings.Contains(string(body), `"feed":"trending"`) {
		t.Errorf("status = %s", body)
	}
	if resp, body := h.do(t, "GET", "/health", ""); resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("health = %d %q", resp.StatusCode, body)
	}
	if _, body := h.do(t, "GET", "/", ""); !strings.Contains(string(body), "dealsieve") {
		t.Errorf("page = %q", body)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/api/`
Expected: FAIL (`undefined: Server`).

- [ ] **Step 3: Implement**

`internal/api/api.go`:
```go
// Package api serves the JSON API and the embedded page.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/poller"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

const (
	listWindow = 72 * time.Hour
	listLimit  = 200
)

type Recomputer interface {
	RecomputeRecent(ctx context.Context) error
}

type Server struct {
	Store     *store.Store
	Recompute Recomputer
	Status    func() []poller.Status
	Config    *config.Config
	Assets    fs.FS
	Log       *slog.Logger
	Now       func() time.Time
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/deals", s.listDeals)
	mux.HandleFunc("POST /api/actions", s.addAction)
	mux.HandleFunc("DELETE /api/actions/{id}", s.deleteAction)
	mux.HandleFunc("GET /api/rules", s.rules)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /", http.FileServerFS(s.Assets))
	return mux
}

type dealJSON struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	Title        string    `json:"title"`
	Merchant     string    `json:"merchant"`
	Price        string    `json:"price"`
	Category     string    `json:"category"`
	ImageURL     string    `json:"image_url"`
	Temperature  *int      `json:"temperature"`
	Temperatures []int     `json:"temperatures"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	InTrending   bool      `json:"in_trending"`
	InNew        bool      `json:"in_new"`
	Decision     string    `json:"decision"`
	Score        int       `json:"score"`
	WatchlistHit bool      `json:"watchlist_hit"`
	Reasons      []string  `json:"reasons"`
}

func (s *Server) listDeals(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = store.ViewPassed
	}
	if !store.ValidView(view) {
		writeError(w, http.StatusBadRequest, "unknown view")
		return
	}
	listed, err := s.Store.ListDeals(r.Context(), view, s.Now().Add(-listWindow), listLimit)
	if err != nil {
		s.fail(w, err)
		return
	}
	ids := make([]string, len(listed))
	for i, l := range listed {
		ids[i] = l.ID
	}
	temps, err := s.Store.Temperatures(r.Context(), ids)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]dealJSON, 0, len(listed))
	for _, l := range listed {
		t := temps[l.ID]
		if t == nil {
			t = []int{}
		}
		reasons := l.Verdict.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		out = append(out, dealJSON{
			ID: l.ID, URL: l.URL, Title: l.Title, Merchant: l.Merchant, Price: l.Price, Category: l.Category,
			ImageURL: l.ImageURL, Temperature: l.Temperature, Temperatures: t, FirstSeen: l.FirstSeen,
			LastSeen: l.LastSeen, InTrending: l.InTrending, InNew: l.InNew, Decision: l.Verdict.Decision,
			Score: l.Verdict.Score, WatchlistHit: l.Verdict.WatchlistHit, Reasons: reasons,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"deals": out})
}

func (s *Server) addAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string `json:"kind"`
		DealID string `json:"deal_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !store.ValidActionKind(req.Kind) {
		writeError(w, http.StatusBadRequest, "unknown action kind")
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
	value := map[string]string{
		store.ActionHide:         deal.ID,
		store.ActionJunk:         deal.Fingerprint,
		store.ActionMuteCategory: deal.Category,
		store.ActionMuteMerchant: deal.Merchant,
	}[req.Kind]
	if value == "" {
		writeError(w, http.StatusBadRequest, "this deal has nothing to "+req.Kind)
		return
	}
	a, err := s.Store.AddAction(r.Context(), req.Kind, value, s.Now())
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Recompute.RecomputeRecent(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) deleteAction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid action id")
		return
	}
	found, err := s.Store.DeleteAction(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "action not found")
		return
	}
	if err := s.Recompute.RecomputeRecent(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rules(w http.ResponseWriter, r *http.Request) {
	actions, err := s.Store.Actions(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if actions == nil {
		actions = []store.Action{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": s.Config.Rules, "watchlist": s.Config.Watchlist, "actions": actions})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"feeds": s.Status()})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.Log.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/api/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "feat(api): JSON API for deals, actions, rules and status" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: The web page

**Files:**
- Create: `web/embed.go`, `web/index.html`, `web/static/css/styles.css`, `web/static/js/api.js`, `web/static/js/render.js`, `web/static/js/sparkline.js`, `web/static/js/format.js`, `web/static/js/theme.js`, `web/static/js/main.js`
- Test: `web/embed_test.go`, `web/static/js/sparkline.test.js`, `web/static/js/format.test.js`

**Interfaces:**
- Consumes: the API in Task 9 (deal JSON fields, endpoints).
- Produces: `web.Assets embed.FS` with `index.html` and `static/...` at its root.

- [ ] **Step 1: Write the failing tests**

`web/embed_test.go`:
```go
package web

import (
	"io/fs"
	"testing"
)

func TestAssetsContainPage(t *testing.T) {
	for _, name := range []string{"index.html", "static/css/styles.css", "static/js/main.js"} {
		if _, err := fs.Stat(Assets, name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
```

`web/static/js/sparkline.test.js`:
```js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sparkPoints } from './sparkline.js';

test('needs at least two values', () => {
  assert.equal(sparkPoints([]), '');
  assert.equal(sparkPoints([42]), '');
});

test('scales values into the 100x24 box', () => {
  assert.equal(sparkPoints([0, 50, 100]), '0,24 50,12 100,0');
});

test('a flat series sits on the bottom', () => {
  assert.equal(sparkPoints([7, 7]), '0,24 100,24');
});
```

`web/static/js/format.test.js`:
```js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ago } from './format.js';

const now = Date.parse('2026-11-03T12:00:00Z');

test('never for missing times', () => assert.equal(ago(null, now), 'never'));
test('just now under a minute', () => assert.equal(ago('2026-11-03T11:59:40Z', now), 'just now'));
test('minutes', () => assert.equal(ago('2026-11-03T11:45:00Z', now), '15 min ago'));
test('hours past two hours', () => assert.equal(ago('2026-11-03T07:00:00Z', now), '5 h ago'));
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./web/ ; node --test web/static/js/sparkline.test.js web/static/js/format.test.js`
Expected: FAIL (Go: `undefined: Assets`; node: cannot find module `./sparkline.js`).

- [ ] **Step 3: Write embed.go and index.html**

`web/embed.go`:
```go
// Package web holds the page and its static files, embedded into the binary.
package web

import "embed"

//go:embed index.html static
var Assets embed.FS
```

`web/index.html`:
```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>dealsieve</title>
  <link rel="stylesheet" href="/static/css/styles.css">
  <script type="module" src="/static/js/main.js"></script>
</head>
<body>
  <header class="top">
    <h1>dealsieve</h1>
    <button id="theme-toggle" type="button" aria-label="Toggle light and dark theme">◐</button>
  </header>
  <div id="stale" class="stale" role="status" hidden></div>
  <nav class="tabs" role="tablist">
    <button type="button" role="tab" data-view="passed" aria-selected="true">Passed</button>
    <button type="button" role="tab" data-view="watchlist" aria-selected="false">Watchlist hits</button>
    <button type="button" role="tab" data-view="blocked" aria-selected="false">Blocked</button>
    <button type="button" role="tab" data-view="rules" aria-selected="false">Rules</button>
  </nav>
  <form id="filters" class="filters">
    <select id="category" aria-label="Category"><option value="">All categories</option></select>
    <input id="search" type="search" placeholder="Search titles" aria-label="Search titles">
  </form>
  <main id="content" aria-live="polite"></main>

  <template id="deal-card">
    <article class="deal">
      <img class="deal-img" alt="" loading="lazy">
      <div class="deal-body">
        <a class="deal-title" target="_blank" rel="noopener noreferrer"></a>
        <p class="deal-meta"><span class="deal-price"></span><span class="deal-merchant"></span><span class="deal-category"></span></p>
        <p class="deal-heat">
          <span class="deal-temp"></span>
          <svg class="spark" viewBox="0 0 100 24" preserveAspectRatio="none" aria-hidden="true">
            <polyline fill="none" stroke="currentColor" stroke-width="2" vector-effect="non-scaling-stroke"/>
          </svg>
        </p>
        <ul class="chips"></ul>
        <div class="actions">
          <button type="button" data-kind="hide_deal">Hide</button>
          <button type="button" data-kind="junk">Junk</button>
          <button type="button" data-kind="mute_category">Mute category</button>
          <button type="button" data-kind="mute_merchant">Mute merchant</button>
        </div>
      </div>
    </article>
  </template>
</body>
</html>
```

- [ ] **Step 4: Write the CSS**

`web/static/css/styles.css`:
```css
:root {
  --bg: #f6f6f3;
  --surface: #ffffff;
  --text: #1c1c1e;
  --muted: #66666d;
  --border: #e0e0dc;
  --accent: #2c6e4c;
  --block: #a33a3a;
  --chip: #e9f1ec;
  --chip-block: #f7e5e5;
  color-scheme: light;
}

@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --bg: #131315;
    --surface: #1c1c1f;
    --text: #ececef;
    --muted: #9b9ba3;
    --border: #2c2c31;
    --accent: #6cc597;
    --block: #e27b7b;
    --chip: #213028;
    --chip-block: #392324;
    color-scheme: dark;
  }
}

:root[data-theme="dark"] {
  --bg: #131315;
  --surface: #1c1c1f;
  --text: #ececef;
  --muted: #9b9ba3;
  --border: #2c2c31;
  --accent: #6cc597;
  --block: #e27b7b;
  --chip: #213028;
  --chip-block: #392324;
  color-scheme: dark;
}

* { box-sizing: border-box; }
[hidden] { display: none !important; }

body {
  margin: 0;
  font: 15px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif;
  background: var(--bg);
  color: var(--text);
}

button {
  font: inherit;
  color: inherit;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 4px 10px;
  cursor: pointer;
}
button:hover { border-color: var(--accent); }

.top { display: flex; align-items: center; justify-content: space-between; padding: 12px 16px; }
.top h1 { font-size: 1.2rem; margin: 0; }

.tabs { display: flex; gap: 4px; padding: 0 16px; overflow-x: auto; }
.tabs [aria-selected="true"] { border-color: var(--accent); color: var(--accent); font-weight: 600; }

.filters { display: flex; flex-wrap: wrap; gap: 8px; padding: 12px 16px; }
.filters select,
.filters input {
  flex: 1 1 160px;
  min-width: 0;
  font: inherit;
  padding: 6px 8px;
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 6px;
}

main { display: grid; gap: 10px; max-width: 900px; padding: 0 16px 32px; }

.deal {
  display: flex;
  gap: 12px;
  padding: 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
}
.deal-blocked { opacity: 0.75; }
.deal-img { flex: none; width: 72px; height: 72px; object-fit: cover; border-radius: 6px; }
.deal-body { flex: 1; min-width: 0; }
.deal-title { color: var(--text); font-weight: 600; text-decoration: none; overflow-wrap: anywhere; }
.deal-title:hover { text-decoration: underline; }
.deal-meta,
.deal-heat { margin: 4px 0; color: var(--muted); font-size: 0.9rem; }
.deal-meta span:not(:empty) ~ span:not(:empty)::before { content: " · "; }
.deal-heat { display: flex; align-items: center; gap: 8px; }
.spark { width: 100px; height: 24px; color: var(--accent); }

.chips { display: flex; flex-wrap: wrap; gap: 6px; margin: 6px 0; padding: 0; list-style: none; }
.chip { padding: 1px 8px; font-size: 0.8rem; background: var(--chip); border-radius: 999px; }
.chip-block { color: var(--block); background: var(--chip-block); }

.actions { display: flex; flex-wrap: wrap; gap: 6px; }
.actions button { font-size: 0.8rem; }

.stale { margin: 0 16px 8px; padding: 8px 12px; color: var(--block); background: var(--chip-block); border-radius: 6px; }
.note { color: var(--muted); }
.error { color: var(--block); }

.rules-yaml {
  padding: 12px;
  overflow-x: auto;
  font-size: 0.85rem;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
}
.action-list { display: grid; gap: 6px; padding: 0; list-style: none; }
.action-list li {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px;
  overflow-wrap: anywhere;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
}
```

- [ ] **Step 5: Write the pure modules**

`web/static/js/sparkline.js`:
```js
// Points for a <polyline> in a 100×24 viewBox, oldest value on the left.
export function sparkPoints(values, width = 100, height = 24) {
  if (!values || values.length < 2) return '';
  const min = Math.min(...values);
  const span = Math.max(...values) - min || 1;
  const step = width / (values.length - 1);
  return values
    .map((v, i) => `${round(i * step)},${round(height - ((v - min) / span) * height)}`)
    .join(' ');
}

const round = (n) => Math.round(n * 10) / 10;
```

`web/static/js/format.js`:
```js
export function ago(iso, now = Date.now()) {
  if (!iso) return 'never';
  const minutes = Math.round((now - Date.parse(iso)) / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 120) return `${minutes} min ago`;
  return `${Math.round(minutes / 60)} h ago`;
}
```

`web/static/js/theme.js`:
```js
const KEY = 'dealsieve-theme';

export function initTheme(button) {
  const saved = read();
  if (saved) document.documentElement.dataset.theme = saved;
  button.addEventListener('click', () => {
    const current = document.documentElement.dataset.theme
      || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
    const next = current === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    write(next);
  });
}

// Storage can be blocked (private windows); the toggle still works per visit.
function read() {
  try { return localStorage.getItem(KEY); } catch { return null; }
}

function write(value) {
  try { localStorage.setItem(KEY, value); } catch { /* ignore */ }
}
```

- [ ] **Step 6: Write the API client and rendering**

`web/static/js/api.js`:
```js
async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let message = res.statusText;
    try { message = (await res.json()).error || message; } catch { /* not JSON */ }
    throw new Error(message);
  }
  return res.status === 204 ? null : res.json();
}

export const getDeals = (view) => request('GET', `/api/deals?view=${encodeURIComponent(view)}`).then((r) => r.deals);
export const addAction = (kind, dealId) => request('POST', '/api/actions', { kind, deal_id: dealId });
export const deleteAction = (id) => request('DELETE', `/api/actions/${encodeURIComponent(id)}`);
export const getRules = () => request('GET', '/api/rules');
export const getStatus = () => request('GET', '/api/status').then((r) => r.feeds);
```

`web/static/js/render.js`:
```js
import { sparkPoints } from './sparkline.js';
import { ago } from './format.js';

const ACTION_LABELS = {
  hide_deal: 'Hidden deal',
  junk: 'Junk pattern',
  mute_category: 'Muted category',
  mute_merchant: 'Muted merchant',
};

export function renderDeals(container, deals, { blockedView, onAction }) {
  if (deals.length === 0) {
    container.replaceChildren(el('p', 'note', 'Nothing here right now.'));
    return;
  }
  const template = document.getElementById('deal-card');
  container.replaceChildren(...deals.map((deal) => dealCard(template, deal, blockedView, onAction)));
}

function dealCard(template, deal, blockedView, onAction) {
  const card = template.content.firstElementChild.cloneNode(true);
  const img = card.querySelector('.deal-img');
  if (deal.image_url) img.src = deal.image_url;
  else img.remove();

  const link = card.querySelector('.deal-title');
  link.href = deal.url;
  link.textContent = deal.title;
  card.querySelector('.deal-price').textContent = deal.price;
  card.querySelector('.deal-merchant').textContent = deal.merchant;
  card.querySelector('.deal-category').textContent = deal.category;
  card.querySelector('.deal-temp').textContent = deal.temperature == null ? '' : `${deal.temperature}°`;
  card.querySelector('.spark polyline').setAttribute('points', sparkPoints(deal.temperatures));

  const chips = card.querySelector('.chips');
  if (deal.in_trending) chips.append(chip('trending'));
  deal.reasons.forEach((reason, i) => {
    chips.append(chip(reason, deal.decision === 'block' && i === 0 ? 'chip-block' : ''));
  });

  for (const button of card.querySelectorAll('.actions button')) {
    const kind = button.dataset.kind;
    if ((kind === 'mute_category' && !deal.category) || (kind === 'mute_merchant' && !deal.merchant)) {
      button.remove();
      continue;
    }
    button.addEventListener('click', () => onAction(kind, deal));
  }
  if (blockedView) card.classList.add('deal-blocked');
  return card;
}

export function renderRules(container, data, onUndo) {
  const list = el('ul', 'action-list');
  if (data.actions.length === 0) list.append(el('li', 'note', 'No clicks yet.'));
  for (const action of data.actions) {
    const undo = el('button', '', 'Undo');
    undo.type = 'button';
    undo.addEventListener('click', () => onUndo(action.id));
    const item = el('li');
    item.append(el('span', '', `${ACTION_LABELS[action.kind] ?? action.kind}: ${action.value}`), undo);
    list.append(item);
  }
  container.replaceChildren(
    el('h2', '', 'Active rules (from config.yaml)'),
    el('pre', 'rules-yaml', JSON.stringify({ rules: data.rules, watchlist: data.watchlist }, null, 2)),
    el('h2', '', 'Your clicks'),
    list,
  );
}

export function renderStale(banner, feeds) {
  const stale = feeds.filter((f) => f.stale);
  banner.hidden = stale.length === 0;
  banner.textContent = stale
    .map((f) => `${f.feed}: last successful poll ${ago(f.last_success)}${f.last_error ? ` (${f.last_error})` : ''}`)
    .join(' · ');
}

export function renderError(container, err) {
  container.replaceChildren(el('p', 'error', `Something went wrong: ${err.message}`));
}

export function fillCategories(select, deals) {
  const current = select.value;
  const categories = [...new Set(deals.map((d) => d.category).filter(Boolean))]
    .sort((a, b) => a.localeCompare(b, 'de'));
  select.replaceChildren(new Option('All categories', ''), ...categories.map((c) => new Option(c, c)));
  select.value = categories.includes(current) ? current : '';
}

function chip(text, extra = '') {
  return el('li', extra ? `chip ${extra}` : 'chip', text);
}

function el(tag, className = '', text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
```

- [ ] **Step 7: Write main.js**

`web/static/js/main.js`:
```js
import * as api from './api.js';
import { fillCategories, renderDeals, renderError, renderRules, renderStale } from './render.js';
import { initTheme } from './theme.js';

const content = document.getElementById('content');
const filters = document.getElementById('filters');
const category = document.getElementById('category');
const search = document.getElementById('search');
const tabs = document.querySelectorAll('[role=tab]');

const state = { view: 'passed', deals: [] };

async function load() {
  try {
    if (state.view === 'rules') {
      filters.hidden = true;
      renderRules(content, await api.getRules(), undo);
      return;
    }
    filters.hidden = false;
    state.deals = await api.getDeals(state.view);
    fillCategories(category, state.deals);
    show();
  } catch (err) {
    renderError(content, err);
  }
}

function show() {
  const q = search.value.trim().toLowerCase();
  const cat = category.value;
  const deals = state.deals.filter((d) => (!cat || d.category === cat) && (!q || d.title.toLowerCase().includes(q)));
  renderDeals(content, deals, { blockedView: state.view === 'blocked', onAction: act });
}

async function act(kind, deal) {
  try {
    await api.addAction(kind, deal.id);
    await load();
  } catch (err) {
    renderError(content, err);
  }
}

async function undo(id) {
  try {
    await api.deleteAction(id);
    await load();
  } catch (err) {
    renderError(content, err);
  }
}

async function refreshStatus() {
  try {
    renderStale(document.getElementById('stale'), await api.getStatus());
  } catch {
    // The deal list shows errors; the banner just stays as it was.
  }
}

for (const tab of tabs) {
  tab.addEventListener('click', () => {
    for (const t of tabs) t.setAttribute('aria-selected', String(t === tab));
    state.view = tab.dataset.view;
    load();
  });
}
category.addEventListener('change', show);
search.addEventListener('input', show);
filters.addEventListener('submit', (e) => e.preventDefault());
initTheme(document.getElementById('theme-toggle'));

load();
refreshStatus();
setInterval(() => { if (state.view !== 'rules') load(); }, 120_000);
setInterval(refreshStatus, 60_000);
```

- [ ] **Step 8: Run the tests**

Run: `go test ./web/ && node --test web/static/js/sparkline.test.js web/static/js/format.test.js`
Expected: Go `ok`; node reports 7 passing tests, 0 failing.

- [ ] **Step 9: Commit**

```bash
git add web
git commit -m "feat(web): vanilla page with tabs, deal cards, sparklines and rule clicks" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Wiring and end-to-end test

**Files:**
- Create: `internal/version/version.go`, `cmd/dealsieve/main.go`
- Test: `test/e2e/e2e_test.go`

**Interfaces:**
- Consumes: everything above (`config.Load`, `store.Open`, `rules.New`, `notify.FromConfig`, `pipeline.Pipeline`, `mydealz.Client`, `poller.New`, `api.Server`, `web.Assets`).
- Produces: the `dealsieve` binary; `dealsieve healthcheck` exits 0 when `GET /health` on `DEALSIEVE_LISTEN` answers 200.

- [ ] **Step 1: Write the failing e2e test**

`test/e2e/e2e_test.go`:
```go
//go:build e2e

// The e2e test builds the real binary and runs it against fake feed and ntfy
// servers. Run it with: go test -tags e2e ./test/e2e/
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type deal struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Reasons []string `json:"reasons"`
}

func TestEndToEnd(t *testing.T) {
	bin := buildBinary(t)

	feeds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss/trending":
			http.ServeFile(w, r, fixture("trending.xml"))
		case "/rss/alles":
			http.ServeFile(w, r, fixture("new.xml"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer feeds.Close()

	var mu sync.Mutex
	var pushes []map[string]any
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		pushes = append(pushes, p)
		mu.Unlock()
	}))
	defer ntfy.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := fmt.Sprintf(`poll: { trending: 1m, new: 1m }
rules:
  min_temperature: 100
  block_keywords: [gratisprobe]
watchlist: [rtx 5080]
notify:
  - when: watchlist
    channels: [push]
channels:
  push: { type: ntfy, url: %q, topic: deals, priority: high }
`, ntfy.URL)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	listen := "127.0.0.1:" + freePort(t)
	env := append(os.Environ(),
		"DEALSIEVE_CONFIG="+cfgPath,
		"DEALSIEVE_DATA_DIR="+filepath.Join(dir, "data"),
		"DEALSIEVE_LISTEN="+listen,
		"DEALSIEVE_MYDEALZ_URL="+feeds.URL,
	)
	var logs bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	base := "http://" + listen

	waitFor(t, "two passed deals", func() bool { return len(getDeals(base, "passed")) == 2 })
	if got := ids(getDeals(base, "passed")); got != "mydealz:1000001,mydealz:1000002" {
		t.Errorf("passed = %s", got)
	}

	waitFor(t, "the watchlist hit", func() bool { return len(getDeals(base, "watchlist")) == 1 })
	if got := getDeals(base, "watchlist")[0]; got.ID != "mydealz:1000010" {
		t.Errorf("watchlist = %+v", got)
	}

	blocked := getDeals(base, "blocked")
	if ids(blocked) != "mydealz:1000003" || !strings.HasPrefix(blocked[0].Reasons[0], "too cold") {
		t.Errorf("blocked = %+v (the new-only keyword deal must not show)", blocked)
	}

	waitFor(t, "the ntfy push", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(pushes) == 1
	})
	mu.Lock()
	if pushes[0]["title"] != "Example RTX 5080 Grafikkarte 16GB" || pushes[0]["priority"] != float64(4) {
		t.Errorf("push = %v", pushes[0])
	}
	mu.Unlock()

	resp, err := http.Post(base+"/api/actions", "application/json", strings.NewReader(`{"kind":"hide_deal","deal_id":"mydealz:1000001"}`))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("hide: %v %v", resp, err)
	}
	resp.Body.Close()
	if got := ids(getDeals(base, "passed")); got != "mydealz:1000002" {
		t.Errorf("passed after hide = %s", got)
	}
	if got := getDeals(base, "blocked"); len(got) != 2 || got[0].ID != "mydealz:1000001" || got[0].Reasons[0] != "hidden by you" {
		t.Errorf("blocked after hide = %+v", got)
	}

	hc := exec.Command(bin, "healthcheck")
	hc.Env = env
	if out, err := hc.CombinedOutput(); err != nil {
		t.Errorf("healthcheck: %v %s", err, out)
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "dealsieve")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, "../../cmd/dealsieve").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func fixture(name string) string {
	return filepath.Join("..", "..", "internal", "source", "mydealz", "testdata", name)
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

func getDeals(base, view string) []deal {
	resp, err := http.Get(base + "/api/deals?view=" + view)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Deals []deal `json:"deals"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	return body.Deals
}

func ids(ds []deal) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return strings.Join(out, ",")
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
```

Why the blocked list after hiding has two entries in that order: 1000001 (score 142) sorts above 1000003 (score -15).

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -tags e2e ./test/e2e/`
Expected: FAIL (`build: … no Go files` / `cannot find package` for `../../cmd/dealsieve`).

- [ ] **Step 3: Write the version package**

`internal/version/version.go`:
```go
// Package version holds the build version, set with
// -ldflags "-X github.com/MorrisMorrison/dealsieve/internal/version.Version=…".
package version

var Version = "dev"
```

- [ ] **Step 4: Write main.go**

`cmd/dealsieve/main.go`:
```go
// Command dealsieve polls deal feeds, filters them and serves the result.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/api"
	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/pipeline"
	"github.com/MorrisMorrison/dealsieve/internal/poller"
	"github.com/MorrisMorrison/dealsieve/internal/rules"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/source/mydealz"
	"github.com/MorrisMorrison/dealsieve/internal/store"
	"github.com/MorrisMorrison/dealsieve/internal/version"
	"github.com/MorrisMorrison/dealsieve/web"
)

const repoURL = "https://github.com/MorrisMorrison/dealsieve"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dealsieve:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := os.Getenv("DEALSIEVE_CONFIG")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath, os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Env.LogFormat)

	if err := os.MkdirAll(cfg.Env.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	st, err := store.Open(filepath.Join(cfg.Env.DataDir, "dealsieve.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	httpc := &http.Client{Timeout: 30 * time.Second}
	pipe := &pipeline.Pipeline{
		Store:     st,
		Engine:    rules.New(cfg.Rules, cfg.Watchlist),
		Notify:    cfg.Notify,
		Notifiers: notify.FromConfig(cfg.Channels, cfg.Env, httpc),
		Now:       time.Now,
		Log:       log,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Apply config changes to recent deals before serving them.
	if err := pipe.RecomputeRecent(ctx); err != nil {
		return fmt.Errorf("recompute verdicts: %w", err)
	}

	src := &mydealz.Client{
		BaseURL:   cfg.Env.MydealzURL,
		UserAgent: "dealsieve/" + version.Version + " (+" + repoURL + ")",
		HTTP:      httpc,
		Log:       log,
	}
	pl := poller.New(src, pipe, map[source.Feed]time.Duration{
		source.FeedTrending: cfg.Poll.Trending.D(),
		source.FeedNew:      cfg.Poll.New.D(),
	}, log)
	go pl.Run(ctx)
	go pruneLoop(ctx, pipe, log)

	srv := &http.Server{
		Addr: cfg.Env.Listen,
		Handler: (&api.Server{
			Store: st, Recompute: pipe, Status: pl.Status, Config: cfg,
			Assets: web.Assets, Log: log, Now: time.Now,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("dealsieve started", "version", version.Version, "listen", cfg.Env.Listen)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func pruneLoop(ctx context.Context, pipe *pipeline.Pipeline, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := pipe.Prune(ctx); err != nil {
			log.Error("prune failed", "err", err)
		} else if n > 0 {
			log.Info("pruned old deals", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func newLogger(format string) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// healthcheck is for the Docker HEALTHCHECK; the image has no curl.
func healthcheck() int {
	addr := os.Getenv("DEALSIEVE_LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
```

- [ ] **Step 5: Run everything**

Run: `go vet ./... && go test ./... && go test -tags e2e ./test/e2e/`
Expected: all `ok`. The e2e test starts and stops its own server.

- [ ] **Step 6: Commit**

```bash
git add internal/version cmd test
git commit -m "feat: wire up the dealsieve binary, with an end-to-end test" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Docker image, CI, README and license

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `.github/workflows/ci.yml`, `.github/workflows/docker-publish.yml`, `config.example.yaml`, `README.md`, `LICENSE`

**Interfaces:**
- Consumes: the binary from Task 11 and its env vars.

- [ ] **Step 1: Write the Dockerfile and .dockerignore**

`Dockerfile`:
```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The release tag (v0.0.1) or dev-<sha>, set by the publish workflow.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/MorrisMorrison/dealsieve/internal/version.Version=${VERSION}" \
    -o /out/dealsieve ./cmd/dealsieve && \
    mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/dealsieve /dealsieve
# A named volume copies this directory's owner, so the nonroot user can write.
COPY --from=build --chown=65532:65532 /out/data /data
ENV DEALSIEVE_CONFIG=/config/config.yaml \
    DEALSIEVE_DATA_DIR=/data \
    DEALSIEVE_LISTEN=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/dealsieve", "healthcheck"]
ENTRYPOINT ["/dealsieve"]
```

`.dockerignore`:
```
.git
.github
data
config.yaml
docs
```

- [ ] **Step 2: Write the workflows**

`.github/workflows/ci.yml`:
```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - name: gofmt
        run: test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)
      - run: go vet ./...
      - run: go test -race ./...
      - name: End-to-end
        run: go test -tags e2e ./test/e2e/
      - uses: actions/setup-node@v5
        with:
          node-version: 24
      - name: Frontend tests
        run: node --test web/static/js/*.test.js

  docker:
    if: github.event_name == 'pull_request'
    needs: check
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: docker/setup-buildx-action@v4
      - uses: docker/build-push-action@v7
        with:
          context: .
          push: false
```

`.github/workflows/docker-publish.yml`:
```yaml
name: Build and Push Docker Image

on:
  push:
    branches: [main]
    tags: ['v*']

env:
  REGISTRY: ghcr.io
  IMAGE_NAME: ${{ github.repository }}

jobs:
  build-and-push:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v7
      - uses: docker/login-action@v3
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      # latest=false: only a push to main moves :latest, so tagging an older
      # commit never rolls it back.
      - id: meta
        uses: docker/metadata-action@v6
        with:
          images: ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}
          flavor: |
            latest=false
          tags: |
            type=raw,value=latest,enable=${{ github.ref == 'refs/heads/main' }}
            type=semver,pattern={{version}}
            type=sha,prefix={{branch}}-,enable=${{ github.ref_type == 'branch' }}
      - uses: docker/build-push-action@v7
        with:
          context: .
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ github.ref_type == 'tag' && github.ref_name || format('dev-{0}', github.sha) }}
```

- [ ] **Step 3: Write config.example.yaml**

`config.example.yaml`:
```yaml
# dealsieve config. Secrets come from environment variables, not this file:
#   DEALSIEVE_TELEGRAM_TOKEN  (needed for telegram channels)
#   DEALSIEVE_NTFY_TOKEN      (optional, for a protected ntfy server)

poll:
  trending: 5m          # the deals shown on the page
  new: 2m               # every new deal, only matched against the watchlist

rules:
  min_temperature: 100
  category_overrides:
    "Elektronik": 60
  muted_categories: ["Fashion & Accessoires", "Verträge"]
  muted_merchants: []
  block_keywords: [gratisprobe, gewinnspiel, cashback]
  recurrence:
    window: 90d         # look back this far for the same offer
    max_seen: 2         # block from the 3rd time it shows up

watchlist: [oled, rtx 5080, lego technic]

notify:
  - when: watchlist
    channels: [ntfy-high]
  - when: { min_temperature: 500 }
    channels: [ntfy-default]

channels:
  ntfy-high:    { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: high }
  ntfy-default: { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: default }
  # telegram:   { type: telegram, chat_id: "123456789" }
```

Add a config test so the example never rots. Append to `internal/config/config_test.go`:
```go
func TestExampleConfigLoads(t *testing.T) {
	if _, err := Load("../../config.example.yaml", env(nil)); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 4: Write README.md**

`README.md`:
````markdown
# dealsieve

A small, self-hosted filter for deal feeds. dealsieve reads a deal community's feeds, throws away
the noise, and shows what's left on a simple web page, with optional push notifications.

Its first (and so far only) source is [mydealz](https://www.mydealz.de). dealsieve is not affiliated
with it; it reads the public RSS feeds, links every deal back to the original post, and is meant for
personal use. Please respect the site's terms and keep polling intervals reasonable.

## What it filters

- **Recurring pseudo-deals.** The same kind of offer posted again and again ("flights from €19.99")
  is recognized by a fingerprint of merchant + title words and blocked after it has been seen a
  few times.
- **Categories, merchants and keywords** you never want to see.
- **Cold deals** below a temperature you choose (per category if you like).
- **Your own feedback:** every deal card has *Hide*, *Junk*, *Mute category* and *Mute merchant*.
  *Junk* blocks everything that looks like that deal from then on. Every click can be undone.

Every decision is shown with its reason, and blocked deals stay visible in their own tab, so you
can see whether a rule is too strict.

## Watchlist and notifications

Watchlist keywords are checked against every new deal as soon as it's posted, before it trends.
Notification rules send watchlist hits, or deals above a temperature, to
[ntfy](https://ntfy.sh) and/or Telegram.

## Run it

```bash
docker run -d --name dealsieve -p 8080:8080 \
  -v dealsieve-data:/data \
  -v "$PWD/config.yaml:/config/config.yaml:ro" \
  ghcr.io/morrismorrison/dealsieve:latest
```

Start from [`config.example.yaml`](config.example.yaml). The page has **no login**: keep it on a
private network or put it behind your reverse proxy's authentication.

With a bind mount instead of a named volume for `/data`, make the directory writable by UID 65532.

## Configuration

| Env var | Default | |
|---|---|---|
| `DEALSIEVE_CONFIG` | `config.yaml` (`/config/config.yaml` in the image) | Path to the YAML config |
| `DEALSIEVE_DATA_DIR` | `./data` (`/data` in the image) | Where the SQLite database lives |
| `DEALSIEVE_LISTEN` | `:8080` | Listen address |
| `DEALSIEVE_LOG_FORMAT` | `text` | `text` or `json` |
| `DEALSIEVE_TELEGRAM_TOKEN` | | Bot token, needed for Telegram channels |
| `DEALSIEVE_NTFY_TOKEN` | | Access token for a protected ntfy server |
| `DEALSIEVE_MYDEALZ_URL` | `https://www.mydealz.de` | Feed host (for tests) |

Config changes apply on restart. Invalid configs are rejected at startup with a list of problems.

## Development

```bash
go test ./...
go test -tags e2e ./test/e2e/
node --test web/static/js/*.test.js
```

The frontend is plain HTML, CSS and ES modules embedded into the binary: no build step.

## License

MIT
````

- [ ] **Step 5: Write LICENSE**

`LICENSE`:
```
MIT License

Copyright (c) 2026 MorrisMorrison

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

- [ ] **Step 6: Verify and scrub**

Run:
```bash
gofmt -l . ; go vet ./... && go test ./... && go test -tags e2e ./test/e2e/ && node --test web/static/js/*.test.js
grep -rni "alternative" --exclude-dir=.git . || echo "clean"
```
Expected: no `gofmt` output, all tests pass, the grep prints `clean`. Then grep the repo for the maintainer's private infrastructure terms (domains, host names, private IP ranges). That list is kept outside this public repo; ask the maintainer for it if you don't have it. If Docker isn't available locally, CI runs the image build on the first PR.

- [ ] **Step 7: Commit**

```bash
git add Dockerfile .dockerignore .github config.example.yaml README.md LICENSE internal/config/config_test.go
git commit -m "chore: Docker image, CI workflows, README, example config and MIT license" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Publish (stop and ask the user first)

Creating a **public** GitHub repo and pushing is outward-facing. **Don't do this step without the user's explicit OK in chat.**

- [ ] **Step 1: Ask the user** whether to create `MorrisMorrison/dealsieve` as a public repo and push `main`.

- [ ] **Step 2: On a yes**, run (needs the `gh` CLI):
```bash
gh repo create MorrisMorrison/dealsieve --public --source=. --push \
  --description "Self-hosted filter for deal feeds: blocks recurring pseudo-deals, shows the rest with reasons"
```
Expected: the repo URL, and the CI + Docker publish workflows start on `main`. Check them with `gh run list --limit 5`; on red, read the logs (`gh run view <id> --log-failed`) and fix on a branch with a PR.

- [ ] **Step 3: Hand off the follow-ups** (separate work, not this plan): the maintainer's own agent setup and personal deployment are tracked outside this repo.
