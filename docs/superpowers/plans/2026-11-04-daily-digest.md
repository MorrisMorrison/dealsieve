# Daily digest Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A once-a-day notification per chosen channel with the watchlist hits and the top passed deals that are new since the previous digest, configured on the Settings page.

**Architecture:** A `digest` block in the versioned settings document; a new pure `internal/digest` package (`Due`, `Compose`) plus a `Runner` that ticks every minute, reads the store, sends through the existing notifiers outside the pipeline mutex, and remembers each channel's last window end in a new `digest_state` table. Telegram gets an HTML body with linked titles; ntfy gets plain text and an optional click URL from `DEALSIEVE_PUBLIC_URL`.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `log/slog`, `time`, `time/tzdata`, `html`), modernc SQLite, vanilla ES modules (no build step), `node --test`.

Spec: `docs/superpowers/specs/2026-11-04-daily-digest-design.md`. Issue #26. Branch `feat/daily-digest`.

## Global Constraints

- Settings defaults: `"digest": { "enabled": false, "time": "18:00", "timezone": "Europe/Berlin", "top": 10, "channels": [] }`.
- `top` is 1–25. Watchlist hits per digest: at most 10. Titles cut to 80 characters with `…`.
- Message limit: the plain body and the HTML text each stay at or under 4000 bytes (safe for Telegram's 4096 characters and ntfy's 4096 bytes); overflow drops top-list lines from the end and adds `… and K more`.
- First digest window for a channel: the 24 hours before the cutoff. Window bounds: `last_cutoff < first_seen <= cutoff`.
- Validation errors are keyed by field path (`digest.time`, `digest.timezone`, `digest.top`, `digest.channels`, `digest.channels[i]`).
- `DEALSIEVE_PUBLIC_URL` is optional; when set it must be an absolute `http(s)` URL.
- Public repo: no personal domains, hosts, chat IDs or topics anywhere (tests use `example.com` / `example.test`).
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Go 1.26 must be on PATH; `go test -race` needs cgo. The checkout uses CRLF; check formatting with `tr -d '\r' < FILE | gofmt -l` style or rely on CI's `gofmt -l`.
- Every task ends with `go vet ./...` and `go test ./...` green.

## File Structure

- `internal/settings/settings.go` — `Digest` type, defaults, decode-from-defaults, normalize, validate, `ParseClock`, `Digest.Location`; embeds tzdata.
- `internal/settings/summary.go` — digest summary line.
- `internal/store/migrations/003_digest.sql`, `internal/store/digest.go` — `digest_state` table, cutoffs, window query.
- `internal/notify/notify.go`, `internal/notify/telegram.go` — `Message.HTML`, Telegram HTML mode.
- `internal/digest/digest.go` — `Item`, `Due`, `Compose` (pure).
- `internal/digest/runner.go` — `Runner` (`Run`, `Tick`, `SendTest`).
- `internal/config/config.go` — `PublicURL`.
- `internal/api/settings.go`, `internal/api/api.go` — `POST /api/settings/digest/test`.
- `cmd/dealsieve/main.go` — start the runner.
- `web/static/js/settings.js`, `web/static/js/settings-model.js`, `web/static/js/api.js` — the Daily digest block.
- Docs: `README.md`, `CLAUDE.md`, `docs-site/src/content/docs/reference/configuration.md`, `docs-site/src/content/docs/guide/how-it-works.md`.

---

### Task 1: Digest settings

**Files:**
- Modify: `internal/settings/settings.go`
- Modify: `internal/settings/summary.go`
- Test: `internal/settings/settings_test.go`, `internal/settings/summary_test.go`

**Interfaces:**
- Produces:
  - `type Digest struct { Enabled bool \`json:"enabled"\`; Time string \`json:"time"\`; Timezone string \`json:"timezone"\`; Top int \`json:"top"\`; Channels []string \`json:"channels"\` }`
  - `Settings.Digest Digest \`json:"digest"\``
  - `func ParseClock(s string) (hour, minute int, ok bool)`
  - `func (d Digest) Location() *time.Location` (UTC if the zone doesn't load)
  - `Decode` now starts from `Defaults()`, so a stored document without `digest` gets the default block.

- [ ] **Step 1: Write the failing tests** (append to `settings_test.go`)

```go
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
```

Append to `summary_test.go`:

```go
func TestSummarizeDigest(t *testing.T) {
	prev := Defaults()
	next := Defaults()
	next.Digest = Digest{Enabled: true, Time: "07:30", Timezone: "Europe/Berlin", Top: 5, Channels: []string{"phone", "tg"}}
	if got := Summarize(prev, next); !slices.Equal(got, []string{"Daily digest at 07:30 (Europe/Berlin) to phone, tg, top 5"}) {
		t.Errorf("on: %q", got)
	}
	if got := Summarize(next, prev); !slices.Equal(got, []string{"Daily digest off"}) {
		t.Errorf("off: %q", got)
	}
	if got := Summarize(prev, Defaults()); len(got) != 0 {
		t.Errorf("unchanged: %q", got)
	}
}
```

(Add `"slices"` to the summary test imports if it isn't there.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/settings/`
Expected: build failure — `Digest` undefined.

- [ ] **Step 3: Implement**

In `settings.go`:

```go
import (
	// ...existing imports...
	"strconv"
	_ "time/tzdata" // the distroless image has no zoneinfo; the digest needs time zones
)

type Settings struct {
	Watchlist   []string           `json:"watchlist"`
	Rules       Rules              `json:"rules"`
	Channels    map[string]Channel `json:"channels"`
	Notify      []NotifyRule       `json:"notify"`
	Suggestions Suggestions        `json:"suggestions"`
	Digest      Digest             `json:"digest"`
}

// Digest is the daily summary: at Time in Timezone, each channel gets the
// watchlist hits and the Top other passed deals that are new since its
// previous digest.
type Digest struct {
	Enabled  bool     `json:"enabled"`
	Time     string   `json:"time"`
	Timezone string   `json:"timezone"`
	Top      int      `json:"top"`
	Channels []string `json:"channels"`
}

// MaxDigestTop keeps a digest inside one notification.
const MaxDigestTop = 25

// Location is the digest's time zone, or UTC if it doesn't load (only
// possible for unvalidated settings).
func (d Digest) Location() *time.Location {
	if loc, err := time.LoadLocation(d.Timezone); err == nil && d.Timezone != "" && d.Timezone != "Local" {
		return loc
	}
	return time.UTC
}

// ParseClock reads a 24-hour "HH:MM" time.
func ParseClock(s string) (hour, minute int, ok bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}
```

In `Defaults()` add `Digest: Digest{Time: "18:00", Timezone: "Europe/Berlin", Top: 10, Channels: []string{}},`.

In `Decode`, replace `var s Settings` with:

```go
	// Start from the defaults so documents saved before a setting existed
	// get its default value.
	s := Defaults()
```

In `Normalize`, before `return out`:

```go
	out.Digest.Time = strings.TrimSpace(s.Digest.Time)
	out.Digest.Timezone = strings.TrimSpace(s.Digest.Timezone)
	out.Digest.Channels = cleanList(s.Digest.Channels)
```

In `Validate`, before `return errs`:

```go
	d := s.Digest
	if _, _, ok := ParseClock(d.Time); !ok {
		errs["digest.time"] = "must be a time like 18:00"
	}
	if _, err := time.LoadLocation(d.Timezone); err != nil || d.Timezone == "" || d.Timezone == "Local" {
		errs["digest.timezone"] = "must be a time zone like Europe/Berlin"
	}
	if d.Top < 1 || d.Top > MaxDigestTop {
		errs["digest.top"] = fmt.Sprintf("must be between 1 and %d", MaxDigestTop)
	}
	if d.Enabled && len(d.Channels) == 0 {
		errs["digest.channels"] = "needs at least one channel"
	}
	for i, c := range d.Channels {
		if _, ok := s.Channels[c]; !ok {
			errs[fmt.Sprintf("digest.channels[%d]", i)] = fmt.Sprintf("unknown channel %q", c)
		}
	}
```

In `summary.go`, in `Summarize` before the suggestions comparison:

```go
	if p, n := prev.Digest, next.Digest; p.Enabled != n.Enabled || p.Time != n.Time || p.Timezone != n.Timezone ||
		p.Top != n.Top || !slices.Equal(p.Channels, n.Channels) {
		out = append(out, digestSummary(n))
	}
```

and add:

```go
func digestSummary(d Digest) string {
	if !d.Enabled {
		return "Daily digest off"
	}
	return fmt.Sprintf("Daily digest at %s (%s) to %s, top %d", d.Time, d.Timezone, strings.Join(d.Channels, ", "), d.Top)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/settings/ ./internal/settingsvc/ ./internal/api/`
Expected: PASS (settingsvc and api decode stored documents; they must still pass).

- [ ] **Step 5: Commit**

```bash
git add internal/settings
git commit -m "Digest settings: schedule, time zone, top N and channels

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Digest state and window query in the store

**Files:**
- Create: `internal/store/migrations/003_digest.sql`
- Create: `internal/store/digest.go`
- Test: `internal/store/digest_test.go`

**Interfaces:**
- Consumes: existing `dealColumns`, `scanDeal`, `fromUnix`, `ErrNotFound` in `internal/store`.
- Produces:
  - `const MaxDigestHits = 10`
  - `func (s *Store) DigestCutoff(ctx context.Context, channel string) (time.Time, bool, error)` — `false` when the channel never had a digest.
  - `func (s *Store) SetDigestCutoff(ctx context.Context, channel string, at time.Time) error`
  - `func (s *Store) DigestDeals(ctx context.Context, since, until time.Time, top int) (hits, others []DealRecord, err error)` — passed deals with `since < first_seen <= until`; hits = watchlist hits newest first (max 10); others = the rest by temperature (unknown last), then newest first (max `top`).

- [ ] **Step 1: Write the failing test** (`internal/store/digest_test.go`)

```go
package store

import (
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func TestDigestCutoff(t *testing.T) {
	s := openTest(t)
	if _, ok, err := s.DigestCutoff(ctx, "phone"); ok || err != nil {
		t.Fatalf("fresh cutoff = %v, %v", ok, err)
	}
	for _, at := range []time.Time{t0, t0.Add(24 * time.Hour)} {
		if err := s.SetDigestCutoff(ctx, "phone", at); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.DigestCutoff(ctx, "phone")
	if !ok || err != nil || !got.Equal(t0.Add(24*time.Hour)) {
		t.Errorf("cutoff = %v, %v, %v", got, ok, err)
	}
	if _, ok, _ := s.DigestCutoff(ctx, "tg"); ok {
		t.Error("cutoffs are per channel")
	}
}

func dealIDs(recs []DealRecord) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}

func TestDigestDeals(t *testing.T) {
	s := openTest(t)
	add := func(id string, temp *int, at time.Time, decision string, watch bool) {
		mustUpsert(t, s, deal(id, temp), source.FeedTrending, "shop|"+id, at)
		save(t, s, "mydealz:"+id, decision, 0, watch)
	}
	since, until := t0.Add(-24*time.Hour), t0
	add("edge-start", intp(900), since, "pass", false)           // excluded: start is exclusive
	add("edge-end", intp(100), until, "pass", false)             // included: end is inclusive
	add("hot", intp(500), t0.Add(-2*time.Hour), "pass", false)   // top 1
	add("warm", intp(300), t0.Add(-3*time.Hour), "pass", false)  // top 2
	add("cold", intp(50), t0.Add(-4*time.Hour), "block", false)  // blocked: excluded
	add("nil", nil, t0.Add(-time.Hour), "pass", false)           // unknown temperature: last
	add("later", intp(999), t0.Add(time.Minute), "pass", false)  // after the window
	add("w-old", intp(10), t0.Add(-5*time.Hour), "pass", true)   // watchlist, older
	add("w-new", intp(10), t0.Add(-30*time.Minute), "pass", true) // watchlist, newer

	hits, others, err := s.DigestDeals(ctx, since, until, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := dealIDs(hits); !slices.Equal(got, []string{"mydealz:w-new", "mydealz:w-old"}) {
		t.Errorf("hits = %v", got)
	}
	if got := dealIDs(others); !slices.Equal(got, []string{"mydealz:hot", "mydealz:warm", "mydealz:edge-end"}) {
		t.Errorf("others = %v", got)
	}
	_, all, _ := s.DigestDeals(ctx, since, until, 25)
	if got := dealIDs(all); got[len(got)-1] != "mydealz:nil" {
		t.Errorf("unknown temperature not last: %v", got)
	}
}

func TestDigestDealsCapsWatchlistHits(t *testing.T) {
	s := openTest(t)
	for i := range MaxDigestHits + 2 {
		id := string(rune('a' + i))
		mustUpsert(t, s, deal(id, nil), source.FeedTrending, "shop|"+id, t0.Add(-time.Duration(i)*time.Minute))
		save(t, s, "mydealz:"+id, "pass", 0, true)
	}
	hits, _, err := s.DigestDeals(ctx, t0.Add(-time.Hour), t0, 10)
	if err != nil || len(hits) != MaxDigestHits {
		t.Errorf("hits = %d, %v", len(hits), err)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/store/`
Expected: build failure — `DigestCutoff` undefined.

- [ ] **Step 3: Implement**

`internal/store/migrations/003_digest.sql`:

```sql
-- When each channel's last successful digest window ended (unix seconds).
CREATE TABLE IF NOT EXISTS digest_state (
    channel     TEXT PRIMARY KEY,
    last_cutoff INTEGER NOT NULL
);
```

`internal/store/digest.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MaxDigestHits caps the watchlist recap in one digest.
const MaxDigestHits = 10

// DigestCutoff returns when the channel's last successful digest window
// ended; false if it never had one.
func (s *Store) DigestCutoff(ctx context.Context, channel string) (time.Time, bool, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT last_cutoff FROM digest_state WHERE channel = ?`, channel).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return fromUnix(at), true, nil
}

func (s *Store) SetDigestCutoff(ctx context.Context, channel string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO digest_state (channel, last_cutoff) VALUES (?, ?)
		ON CONFLICT (channel) DO UPDATE SET last_cutoff = excluded.last_cutoff`, channel, at.Unix())
	return err
}

// DigestDeals returns the passed deals first seen in (since, until]: the
// watchlist hits newest first (at most MaxDigestHits) and the others by
// temperature, unknown last, then newest first (at most top).
func (s *Store) DigestDeals(ctx context.Context, since, until time.Time, top int) (hits, others []DealRecord, err error) {
	const base = `SELECT ` + dealColumns + ` FROM deals d JOIN verdicts v ON v.deal_id = d.id
		WHERE v.decision = 'pass' AND v.watchlist_hit = ? AND d.first_seen > ? AND d.first_seen <= ? `
	hits, err = s.digestQuery(ctx, base+`ORDER BY d.first_seen DESC, d.id LIMIT ?`, 1, since, until, MaxDigestHits)
	if err != nil {
		return nil, nil, err
	}
	others, err = s.digestQuery(ctx, base+`ORDER BY d.temperature IS NULL, d.temperature DESC, d.first_seen DESC, d.id LIMIT ?`, 0, since, until, top)
	return hits, others, err
}

func (s *Store) digestQuery(ctx context.Context, query string, watch int, since, until time.Time, limit int) ([]DealRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, watch, since.Unix(), until.Unix(), limit)
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
```

(If `scanDeal` requires a non-variadic call shape, match how `DealsSeenSince` in `deals.go` calls it. `dealColumns` must be a `const`; if it is a `var`, build the query string with `+` at call time instead of in a `const`.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/`
Expected: PASS (including `TestOpenIsRepeatable`, which re-runs migrations).

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Store digest cutoffs and the digest window query

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: HTML messages for Telegram

**Files:**
- Modify: `internal/notify/notify.go` (the `Message` struct)
- Modify: `internal/notify/telegram.go`
- Test: `internal/notify/notify_test.go`

**Interfaces:**
- Produces: `Message.HTML string` — when set, Telegram sends it as the whole message text with `parse_mode: HTML` (title, body and links included; `ClickURL` and `ImageURL` are not added). ntfy ignores it.

- [ ] **Step 1: Write the failing test** (append to `notify_test.go`)

```go
func TestTelegramSendsHTML(t *testing.T) {
	srv, calls := telegramServer(t, 0)
	tg := &Telegram{APIBase: srv.URL, Token: "TOKEN", ChatID: "42", HTTP: srv.Client()}
	html := `<b>dealsieve · 1 new deal</b>` + "\n" + `<a href="https://d.example.test">A &amp; B</a>`
	if err := tg.Send(ctx, Message{Title: "plain", Body: "plain", ClickURL: "https://x.example.test", HTML: html}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %+v", *calls)
	}
	c := (*calls)[0]
	if c.path != "/botTOKEN/sendMessage" || c.payload["text"] != html || c.payload["parse_mode"] != "HTML" {
		t.Errorf("call = %+v", c)
	}
}

func TestNtfyIgnoresHTML(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	n := &Ntfy{BaseURL: srv.URL, Topic: "deals", HTTP: srv.Client()}
	if err := n.Send(ctx, Message{Title: "T", Body: "plain body", HTML: "<b>x</b>"}); err != nil {
		t.Fatal(err)
	}
	if got["message"] != "plain body" {
		t.Errorf("ntfy got %v", got)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/notify/`
Expected: build failure — unknown field `HTML`.

- [ ] **Step 3: Implement**

In `notify.go`, extend `Message`:

```go
type Message struct {
	Title    string
	Body     string
	ClickURL string
	ImageURL string
	// HTML, when set, is the complete message for channels that render
	// HTML (Telegram): title, body and links. Others use Title and Body.
	HTML string
}
```

In `telegram.go`, at the top of `Send`:

```go
	if m.HTML != "" {
		return t.call(ctx, "sendMessage", map[string]string{"chat_id": t.ChatID, "text": m.HTML, "parse_mode": "HTML"})
	}
```

(The HTML is not truncated: cutting it could break a tag. Its producer keeps it under the limit.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/notify/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notify
git commit -m "Telegram sends an HTML message when one is given

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: `digest` package — when it's due and what it says

**Files:**
- Create: `internal/digest/digest.go`
- Test: `internal/digest/digest_test.go`

**Interfaces:**
- Consumes: `settings.Digest`, `settings.ParseClock`, `Digest.Location()` (Task 1); `notify.Message` with `HTML` (Task 3).
- Produces:
  - `type Item struct { Title, URL, Price string; Temperature *int }`
  - `func Due(now time.Time, d settings.Digest, last time.Time, hasLast bool) bool`
  - `func Compose(hits, top []Item, publicURL string) notify.Message`
  - `const MaxBytes = 4000`

- [ ] **Step 1: Write the failing tests** (`internal/digest/digest_test.go`)

```go
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
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/digest/`
Expected: build failure — package has no non-test files / `Due` undefined.

- [ ] **Step 3: Implement** (`internal/digest/digest.go`)

```go
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/digest/`
Expected: PASS. If the spring-forward `night` case fails, check that `time.Date` normalised 02:30 to 03:30 — it does in Go; the comparison `local.Before(sendAt)` must use the normalised value.

- [ ] **Step 5: Commit**

```bash
git add internal/digest
git commit -m "digest: decide when a digest is due and compose it

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Runner, `DEALSIEVE_PUBLIC_URL` and startup wiring

**Files:**
- Create: `internal/digest/runner.go`
- Test: `internal/digest/runner_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `cmd/dealsieve/main.go`

**Interfaces:**
- Consumes: `store.DigestCutoff`, `store.SetDigestCutoff`, `store.DigestDeals`, `store.DealRecord` (Task 2); `Due`, `Compose`, `Item` (Task 4); `settings.Settings`, `settings.Warnings`; `notify.FromSettings`, `notify.Reason`; `config.Env`.
- Produces:
  - `config.Env.PublicURL string` (from `DEALSIEVE_PUBLIC_URL`).
  - `type TestResult struct { Channel string \`json:"channel"\`; OK bool \`json:"ok"\`; Error string \`json:"error,omitempty"\` }`
  - `type Runner struct { Store *store.Store; Settings func() settings.Settings; Env config.Env; HTTP *http.Client; Notifiers func(map[string]settings.Channel) map[string]notify.Notifier; Now func() time.Time; Log *slog.Logger }`
  - `func (r *Runner) Run(ctx context.Context)` — ticks every minute until ctx ends.
  - `func (r *Runner) Tick(ctx context.Context)`
  - `func (r *Runner) SendTest(ctx context.Context) []TestResult`

- [ ] **Step 1: Write the failing tests**

`internal/config/config_test.go` (append):

```go
func TestPublicURL(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "DEALSIEVE_PUBLIC_URL" {
				return v
			}
			return ""
		}
	}
	e, err := FromEnv(env("https://deals.example.com/"))
	if err != nil || e.PublicURL != "https://deals.example.com" {
		t.Errorf("PublicURL = %q, %v", e.PublicURL, err)
	}
	if e, err := FromEnv(env("")); err != nil || e.PublicURL != "" {
		t.Errorf("unset = %q, %v", e.PublicURL, err)
	}
	for _, bad := range []string{"deals.example.com", "ftp://x.example.com", "https://"} {
		if _, err := FromEnv(env(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
```

`internal/digest/runner_test.go`:

```go
package digest

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
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
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

type runnerHarness struct {
	r     *Runner
	st    *store.Store
	now   *time.Time
	s     *settings.Settings
	phone *fakeNotifier
	tg    *fakeNotifier
}

func newRunner(t *testing.T) *runnerHarness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := at("2026-11-04", "18:00").UTC()
	s := settings.Defaults()
	s.Channels = map[string]settings.Channel{
		"phone": {Type: "ntfy", URL: "https://ntfy.example.com", Topic: "deals"},
		"tg":    {Type: "telegram", ChatID: "1"},
	}
	s.Digest = settings.Digest{Enabled: true, Time: "18:00", Timezone: "Europe/Berlin", Top: 10, Channels: []string{"phone", "tg"}}
	h := &runnerHarness{st: st, now: &now, s: &s, phone: &fakeNotifier{}, tg: &fakeNotifier{}}
	h.r = &Runner{
		Store:    st,
		Settings: func() settings.Settings { return *h.s },
		Env:      config.Env{TelegramToken: "x", PublicURL: "https://deals.example.com"},
		Notifiers: func(map[string]settings.Channel) map[string]notify.Notifier {
			return map[string]notify.Notifier{"phone": h.phone, "tg": h.tg}
		},
		Now: func() time.Time { return *h.now },
		Log: slog.New(slog.DiscardHandler),
	}
	return h
}

func (h *runnerHarness) seed(t *testing.T, id string, temp int, firstSeen time.Time, watch bool) {
	t.Helper()
	d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
		Title: "Deal " + id, Merchant: "Example Shop", Price: "9€", Temperature: &temp}
	if _, err := h.st.UpsertDeal(ctx, d, source.FeedTrending, "deal", "shop|"+id, firstSeen); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SaveVerdict(ctx, store.Verdict{DealID: d.ID, Decision: "pass", Score: temp, WatchlistHit: watch, DecidedAt: firstSeen}); err != nil {
		t.Fatal(err)
	}
}

func TestTickSendsOncePerDay(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-2*time.Hour), false)
	h.seed(t, "2", 50, h.now.Add(-time.Hour), true)
	h.seed(t, "old", 900, h.now.Add(-30*time.Hour), false) // before the first 24 h window

	h.r.Tick(ctx)
	h.r.Tick(ctx) // same minute: nothing more
	if len(h.phone.sent) != 1 || len(h.tg.sent) != 1 {
		t.Fatalf("sent phone=%d tg=%d, want 1 each", len(h.phone.sent), len(h.tg.sent))
	}
	m := h.phone.sent[0]
	if m.Title != "dealsieve · 2 new deals" || !strings.Contains(m.Body, "Deal 2") || strings.Contains(m.Body, "Deal old") {
		t.Errorf("message = %+v", m)
	}

	// Next day: only what is new since the previous digest.
	*h.now = h.now.Add(24 * time.Hour)
	h.seed(t, "3", 200, h.now.Add(-time.Hour), false)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 2 || strings.Contains(h.phone.sent[1].Body, "Deal 1") || !strings.Contains(h.phone.sent[1].Body, "Deal 3") {
		t.Errorf("second digest = %+v", h.phone.sent)
	}
}

func TestTickRetriesOnlyTheFailedChannel(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	h.tg.fail = true
	h.r.Tick(ctx)
	h.tg.fail = false
	*h.now = h.now.Add(time.Minute)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 1 || len(h.tg.sent) != 1 {
		t.Errorf("sent phone=%d tg=%d, want 1 each", len(h.phone.sent), len(h.tg.sent))
	}
}

func TestTickEmptyWindowMovesTheCutoff(t *testing.T) {
	h := newRunner(t)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 0 {
		t.Fatalf("sent %d for an empty window", len(h.phone.sent))
	}
	if last, ok, _ := h.st.DigestCutoff(ctx, "phone"); !ok || !last.Equal(*h.now) {
		t.Errorf("cutoff = %v, %v", last, ok)
	}
}

func TestTickRespectsSettings(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	h.s.Digest.Enabled = false
	h.r.Tick(ctx)
	h.s.Digest.Enabled = true
	*h.now = h.now.Add(-time.Hour) // 17:00 local: not yet
	h.r.Tick(ctx)
	if len(h.phone.sent) != 0 {
		t.Errorf("sent %d", len(h.phone.sent))
	}
}

func TestSendTestDoesNotMoveTheCutoff(t *testing.T) {
	h := newRunner(t)
	*h.now = h.now.Add(-6 * time.Hour) // midday: a real digest isn't due
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	res := h.r.SendTest(ctx)
	if len(res) != 2 || !res[0].OK || !res[1].OK || len(h.phone.sent) != 1 {
		t.Fatalf("results = %+v, sent %d", res, len(h.phone.sent))
	}
	if _, ok, _ := h.st.DigestCutoff(ctx, "phone"); ok {
		t.Error("test send stored a cutoff")
	}
	h.tg.fail = true
	*h.now = h.now.Add(24 * time.Hour) // nothing new in the last 24 h
	res = h.r.SendTest(ctx)
	if !strings.HasPrefix(h.phone.sent[1].Title, "dealsieve · nothing new") || res[1].OK || res[1].Error != "boom" {
		t.Errorf("empty test = %+v / %+v", h.phone.sent[1], res)
	}
}

func TestSendTestWarnsAboutMissingTelegramToken(t *testing.T) {
	h := newRunner(t)
	h.r.Env.TelegramToken = ""
	res := h.r.SendTest(ctx)
	if res[1].Channel != "tg" || res[1].OK || !strings.Contains(res[1].Error, "DEALSIEVE_TELEGRAM_TOKEN") || len(h.tg.sent) != 0 {
		t.Errorf("results = %+v", res)
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/config/ ./internal/digest/`
Expected: build failures — `PublicURL`, `Runner` undefined.

- [ ] **Step 3: Implement**

`internal/config/config.go`: add `PublicURL string` to `Env`; add `"net/url"` and `"strings"` imports; after building `e`:

```go
	if v := getenv("DEALSIEVE_PUBLIC_URL"); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("DEALSIEVE_PUBLIC_URL: must be an absolute http(s) URL, got %q", v))
		} else {
			e.PublicURL = strings.TrimRight(v, "/")
		}
	}
```

`internal/digest/runner.go`:

```go
package digest

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

// firstWindow is how far the first digest of a channel reaches back.
const firstWindow = 24 * time.Hour

type TestResult struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// Runner sends the daily digest. It holds no lock of the pipeline: a slow
// target delays only the digest.
type Runner struct {
	Store    *store.Store
	Settings func() settings.Settings
	Env      config.Env
	HTTP     *http.Client
	// Notifiers builds the channel senders; nil means notify.FromSettings.
	Notifiers func(map[string]settings.Channel) map[string]notify.Notifier
	Now       func() time.Time
	Log       *slog.Logger
}

func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) notifiers(s settings.Settings) map[string]notify.Notifier {
	if r.Notifiers != nil {
		return r.Notifiers(s.Channels)
	}
	return notify.FromSettings(s.Channels, r.Env, r.HTTP)
}

// Tick sends every channel's digest that is due. A failed send stores
// nothing, so the next tick tries again; an empty window moves the cutoff
// without sending.
func (r *Runner) Tick(ctx context.Context) {
	s := r.Settings()
	if !s.Digest.Enabled {
		return
	}
	now := r.Now()
	senders := r.notifiers(s)
	for _, ch := range s.Digest.Channels {
		last, hasLast, err := r.Store.DigestCutoff(ctx, ch)
		if err != nil {
			r.Log.Error("digest cutoff", "channel", ch, "err", err)
			continue
		}
		if !Due(now, s.Digest, last, hasLast) {
			continue
		}
		msg, empty, err := r.compose(ctx, s.Digest, last, hasLast, now)
		if err != nil {
			r.Log.Error("digest deals", "channel", ch, "err", err)
			continue
		}
		if !empty {
			n := senders[ch]
			if n == nil {
				r.Log.Warn("digest channel can't send", "channel", ch)
				continue
			}
			if err := n.Send(ctx, msg); err != nil {
				r.Log.Warn("digest failed, retrying next minute", "channel", ch, "err", notify.Reason(err))
				continue
			}
			r.Log.Info("digest sent", "channel", ch)
		}
		if err := r.Store.SetDigestCutoff(ctx, ch, now); err != nil {
			r.Log.Error("store digest cutoff", "channel", ch, "err", err)
		}
	}
}

// SendTest sends each digest channel its digest for the current window
// right away, without moving the window.
func (r *Runner) SendTest(ctx context.Context) []TestResult {
	s := r.Settings()
	now := r.Now()
	senders := r.notifiers(s)
	warnings := settings.Warnings(s, r.Env.TelegramToken != "")
	var out []TestResult
	for _, ch := range s.Digest.Channels {
		res := TestResult{Channel: ch}
		if w := warnings[ch]; w != "" {
			res.Error = w
			out = append(out, res)
			continue
		}
		last, hasLast, err := r.Store.DigestCutoff(ctx, ch)
		var msg notify.Message
		var empty bool
		if err == nil {
			msg, empty, err = r.compose(ctx, s.Digest, last, hasLast, now)
		}
		switch {
		case err != nil:
			res.Error = "couldn't read the deals"
			r.Log.Error("digest test", "channel", ch, "err", err)
		case senders[ch] == nil:
			res.Error = "channel can't send"
		default:
			if empty {
				msg = emptyMessage(windowStart(last, hasLast, now), s.Digest.Location(), r.Env.PublicURL)
			}
			if err := senders[ch].Send(ctx, msg); err != nil {
				res.Error = notify.Reason(err)
			} else {
				res.OK = true
			}
		}
		out = append(out, res)
	}
	return out
}

func windowStart(last time.Time, hasLast bool, now time.Time) time.Time {
	if hasLast {
		return last
	}
	return now.Add(-firstWindow)
}

func (r *Runner) compose(ctx context.Context, d settings.Digest, last time.Time, hasLast bool, now time.Time) (notify.Message, bool, error) {
	hits, top, err := r.Store.DigestDeals(ctx, windowStart(last, hasLast, now), now, d.Top)
	if err != nil {
		return notify.Message{}, false, err
	}
	if len(hits) == 0 && len(top) == 0 {
		return notify.Message{}, true, nil
	}
	return Compose(items(hits), items(top), r.Env.PublicURL), false, nil
}

func items(recs []store.DealRecord) []Item {
	out := make([]Item, len(recs))
	for i, rec := range recs {
		out[i] = Item{Title: rec.Title, URL: rec.URL, Price: rec.Price, Temperature: rec.Temperature}
	}
	return out
}

func emptyMessage(since time.Time, loc *time.Location, publicURL string) notify.Message {
	title := "dealsieve · nothing new since " + since.In(loc).Format("Mon 15:04")
	body := "This is a test digest. The real one only goes out when there are new deals."
	return notify.Message{Title: title, Body: body, ClickURL: publicURL,
		HTML: "<b>" + title + "</b>\n\n" + body}
}
```

(Field names on `store.DealRecord`: check `internal/store/deals.go` — `Title`, `URL`, `Price`, `Temperature *int` exist there.)

`cmd/dealsieve/main.go`: import `"github.com/MorrisMorrison/dealsieve/internal/digest"`; after the prune loop starts:

```go
	dg := &digest.Runner{
		Store: st, Settings: func() settings.Settings { return svc.Current().Settings },
		Env: env, HTTP: notifyClient, Now: time.Now, Log: log,
	}
	wg.Go(func() { dg.Run(ctx) })
```

(import `internal/settings` too). Pass `dg` to the API server in Task 6.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config/ ./internal/digest/ ./cmd/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/digest cmd/dealsieve
git commit -m "Digest runner: send due digests every minute, DEALSIEVE_PUBLIC_URL

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Test-digest endpoint

**Files:**
- Modify: `internal/api/api.go` (Server field + route)
- Modify: `internal/api/settings.go` (handler)
- Modify: `cmd/dealsieve/main.go` (pass the runner)
- Modify: `web/static/js/api.js`
- Test: `internal/api/settings_test.go`

**Interfaces:**
- Consumes: `digest.TestResult`, `(*digest.Runner).SendTest` (Task 5).
- Produces:
  - `type DigestTester interface { SendTest(ctx context.Context) []digest.TestResult }`; `Server.Digest DigestTester`.
  - `POST /api/settings/digest/test` → `200 {"results":[...]}`; `422 {"error":"..."}` when the saved digest has no channels.
  - JS: `export const testDigest = () => request('POST', '/api/settings/digest/test').then((r) => r.results);`

- [ ] **Step 1: Write the failing test** (append to `internal/api/settings_test.go`)

```go
type fakeDigest struct{ calls int }

func (f *fakeDigest) SendTest(context.Context) []digest.TestResult {
	f.calls++
	return []digest.TestResult{{Channel: "phone", OK: true}, {Channel: "tg", Error: "telegram sendMessage: status 400"}}
}

func TestTestDigest(t *testing.T) {
	h := newHarness(t)
	fd := &fakeDigest{}
	h.api.Digest = fd
	resp, body := h.do(t, "POST", "/api/settings/digest/test", "")
	if resp.StatusCode != http.StatusUnprocessableEntity || fd.calls != 0 {
		t.Fatalf("no digest channels: %d %s", resp.StatusCode, body)
	}

	s := h.svc.Current().Settings
	s.Channels = map[string]settings.Channel{"phone": {Type: "ntfy", URL: "https://ntfy.example.com", Topic: "deals"}}
	s.Digest.Channels = []string{"phone"}
	if _, err := h.svc.Save(context.Background(), h.svc.Current().Version, s); err != nil {
		t.Fatal(err)
	}
	resp, body = h.do(t, "POST", "/api/settings/digest/test", "")
	if resp.StatusCode != 200 || fd.calls != 1 ||
		!strings.Contains(string(body), `{"channel":"phone","ok":true}`) || !strings.Contains(string(body), `"error":"telegram sendMessage: status 400"`) {
		t.Errorf("test digest %d: %s", resp.StatusCode, body)
	}
}
```

(Add imports `"context"`, `"github.com/MorrisMorrison/dealsieve/internal/digest"`, `"github.com/MorrisMorrison/dealsieve/internal/settings"` if missing.)

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/api/`
Expected: build failure — `h.api.Digest` undefined.

- [ ] **Step 3: Implement**

`api.go`: add to `Server`:

```go
	Digest    DigestTester
```

and the interface next to `Recomputer`:

```go
type DigestTester interface {
	SendTest(ctx context.Context) []digest.TestResult
}
```

Route, after `POST /api/settings/test`:

```go
	mux.HandleFunc("POST /api/settings/digest/test", s.testDigest)
```

`settings.go`:

```go
// testDigest sends the saved digest to its channels now, without counting
// as the day's digest.
func (s *Server) testDigest(w http.ResponseWriter, r *http.Request) {
	if s.Digest == nil || len(s.Settings.Current().Settings.Digest.Channels) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "Add a channel to the daily digest and save first.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": s.Digest.SendTest(r.Context())})
}
```

`main.go`: set `Digest: dg,` in the `api.Server` literal (move the runner's construction above the server).

`web/static/js/api.js`: add after `testChannel`:

```js
export const testDigest = () => request('POST', '/api/settings/digest/test').then((r) => r.results);
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/api/ ./cmd/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api cmd/dealsieve web/static/js/api.js
git commit -m "API: send a test digest

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Settings page block and docs

**Files:**
- Modify: `web/static/js/settings.js` (`renderNotifications`, channel removal, imports)
- Modify: `web/static/js/settings-model.js` (`changeCount`, `TIMEZONES`)
- Test: `web/static/js/settings-model.test.js`
- Modify: `web/static/css/*.css` only if the existing classes (`field`, `label`, `row`, `chip`, `rules`, `status-ok`, `status-err`, `help`) don't lay it out acceptably
- Modify: `README.md`, `CLAUDE.md`, `docs-site/src/content/docs/reference/configuration.md`, `docs-site/src/content/docs/guide/how-it-works.md`

**Interfaces:**
- Consumes: `testDigest` (Task 6); the `digest` settings block (Task 1).
- Produces: `TIMEZONES` (array of zone names) and digest-aware `changeCount` in `settings-model.js`.

- [ ] **Step 1: Write the failing node test** (append to `settings-model.test.js`, matching its existing `import`/`test` style)

```js
test('changeCount counts a digest change once', () => {
  const saved = {
    watchlist: [], rules: { muted_merchants: [], muted_categories: [], block_keywords: [], min_temperature: 100, category_overrides: {}, recurrence: { window_days: 90, max_seen: 2 } },
    channels: {}, notify: [], suggestions: { min_count: 3, window_days: 30 },
    digest: { enabled: false, time: '18:00', timezone: 'Europe/Berlin', top: 10, channels: [] },
  };
  const draft = clone(saved);
  draft.digest.enabled = true;
  draft.digest.time = '07:30';
  assert.equal(changeCount(saved, draft), 1);
});

test('TIMEZONES offers common zones', () => {
  assert.ok(TIMEZONES.includes('Europe/Berlin'));
  assert.ok(TIMEZONES.includes('UTC'));
});
```

(Import `TIMEZONES` alongside the existing imports.)

- [ ] **Step 2: Run to see it fail**

Run: `node --test web/static/js/*.test.js`
Expected: FAIL — `TIMEZONES` not exported / count 0.

- [ ] **Step 3: Implement the model**

In `settings-model.js`, in `changeCount` after the suggestions line:

```js
  if (!same(saved.digest, draft.digest)) n++;
```

and add:

```js
// Offered in the digest's time zone field; any IANA zone can be typed.
export const TIMEZONES = [
  'Europe/Berlin', 'Europe/Vienna', 'Europe/Zurich', 'Europe/Paris', 'Europe/Madrid', 'Europe/Rome',
  'Europe/Amsterdam', 'Europe/Brussels', 'Europe/London', 'Europe/Dublin', 'Europe/Lisbon', 'Europe/Warsaw',
  'America/New_York', 'America/Chicago', 'America/Los_Angeles', 'Asia/Tokyo', 'Australia/Sydney', 'UTC',
];
```

- [ ] **Step 4: Implement the page block**

In `settings.js`: import `testDigest` from `./api.js` and `TIMEZONES` from `./settings-model.js`.

In `channelCard`'s Remove handler, after the notify loop:

```js
            st.draft.digest.channels = removeFrom(st.draft.digest.channels, name);
```

Add the block builder:

```js
function digestBlock() {
  const d = st.draft.digest;
  const pred = under('digest');
  const bad = (field) => Object.keys(st.errors).some((p) => p === `digest.${field}` || p.startsWith(`digest.${field}[`));
  const free = Object.keys(st.draft.channels).filter((c) => !d.channels.includes(c));
  const status = h('span', { className: 'status-ok', hidden: true });
  const test = h('button', {
    type: 'button', className: 'btn small', textContent: 'Send test digest',
    on: {
      click: async () => {
        test.disabled = true;
        test.textContent = 'Sending…';
        status.hidden = true;
        let text;
        let ok = false;
        try {
          const results = await testDigest();
          ok = results.every((r) => r.ok);
          text = results.map((r) => `${r.channel}: ${r.ok ? '✓ delivered' : `✗ ${r.error || 'failed'}`}`).join(' · ');
        } catch (err) {
          text = `✗ ${err.message}`;
        }
        test.textContent = 'Send test digest';
        test.disabled = false;
        status.hidden = false;
        status.className = ok ? 'status-ok' : 'status-err';
        status.textContent = text;
      },
    },
  });
  return h('div', { className: 'field' },
    h('span', { className: 'label', textContent: 'Daily digest' }),
    h('p', { className: 'help', textContent: 'Once a day: your watchlist hits and the top deals that are new since the previous digest. Skipped when there is nothing new. The instant notifications above keep working.' }),
    h('label', { className: 'row' },
      h('input', { type: 'checkbox', checked: d.enabled, on: { change: (e) => { d.enabled = e.target.checked; clearErrors(pred); updateBar(); } } }),
      ' Send a daily digest'),
    h('div', { className: 'row' },
      h('span', { className: 'kw', textContent: 'at' }),
      h('input', {
        type: 'time', value: d.time, className: bad('time') ? 'invalid' : '', attrs: { 'aria-label': 'Digest time' }, style: 'width:auto',
        on: { input: (e) => { d.time = e.target.value; clearErrors(pred, e.target); updateBar(); } },
      }),
      h('span', { className: 'kw', textContent: 'time zone' }),
      h('input', {
        type: 'text', value: d.timezone, className: bad('timezone') ? 'invalid' : '', style: 'width:14em',
        attrs: { 'aria-label': 'Digest time zone', list: 'timezones' },
        on: { input: (e) => { d.timezone = e.target.value; clearErrors(pred, e.target); updateBar(); } },
      }),
      h('datalist', { id: 'timezones' }, TIMEZONES.map((z) => h('option', { value: z }))),
      h('span', { className: 'kw', textContent: 'top' }),
      numberInput(d.top, (v) => { d.top = v; }, { min: 1, label: 'Deals in the digest', width: '70px', pred: (p) => p === 'digest.top' })),
    h('div', { className: 'row' },
      h('span', { className: 'kw', textContent: 'send to' }),
      d.channels.map((c) => h('span', { className: 'chip' }, c, h('button', {
        type: 'button', textContent: '×', attrs: { 'aria-label': `Stop sending the digest to ${c}` },
        on: { click: () => { d.channels = removeFrom(d.channels, c); clearErrors(pred); touch('notifications'); } },
      }))),
      free.length ? h('select', {
        attrs: { 'aria-label': 'Add a channel to the digest' }, style: 'width:auto',
        on: { change: (e) => { if (!e.target.value) return; d.channels = [...d.channels, e.target.value]; clearErrors(pred); touch('notifications'); } },
      }, h('option', { value: '', textContent: '+ channel' }), free.map((c) => h('option', { value: c, textContent: c }))) : null),
    errorNodes(pred),
    h('div', { className: 'row' }, test, h('span', { className: 'help', textContent: 'Uses the saved settings.' }), status));
}
```

and append `digestBlock()` as the last element of the array returned by `renderNotifications()`.

- [ ] **Step 5: Check the page in a browser**

Run the built binary locally with a temp data dir (from the repo root; Go 1.26 must be on PATH):

```bash
go build -o /tmp/dealsieve.exe ./cmd/dealsieve && DEALSIEVE_DATA_DIR="$(mktemp -d)" DEALSIEVE_LISTEN=127.0.0.1:8099 DEALSIEVE_MYDEALZ_URL=http://127.0.0.1:1 /tmp/dealsieve.exe
```

Open `http://127.0.0.1:8099/settings.html#notifications` (in the built-in browser), then check: the block renders in light and dark, at 375 px width it wraps without horizontal scroll, turning it on with no channel and saving shows "needs at least one channel" under the block, an invalid zone (`Mars/Olympus`) shows its error, adding an ntfy channel then adding it to the digest and saving works, removing that channel removes it from the digest, the save bar counts the digest change once, and "Send test digest" before saving channels shows the 422 message. Stop the binary afterwards.

- [ ] **Step 6: Docs**

- `README.md`: env table row `| \`DEALSIEVE_PUBLIC_URL\` | unset | The address you open dealsieve at, e.g. \`https://deals.example.com\`. The daily digest links to it. |`; mention the daily digest in the features list next to notifications.
- `docs-site/src/content/docs/reference/configuration.md`: a "Daily digest" settings table (Enabled `off`; Time `18:00`, `HH:MM`; Time zone `Europe/Berlin`, IANA name; Top `10`, 1–25; Channels, at least one when enabled) plus the `DEALSIEVE_PUBLIC_URL` env row and a line on "Send test digest" using the saved settings.
- `docs-site/src/content/docs/guide/how-it-works.md`: under notifications, a short "Daily digest" paragraph: window since the previous digest (24 h for the first), watchlist recap (max 10) then top N by temperature, each deal once, skipped when empty, a failed send retried every minute and folded into the next day if it keeps failing.
- `CLAUDE.md`: add `internal/digest/` to the layout table ("Daily digest: `Due`, `Compose`, and the `Runner` that sends due digests every minute outside the pipeline lock; per-channel window ends in `digest_state`"); add `DEALSIEVE_PUBLIC_URL` wherever env vars are listed.

- [ ] **Step 7: Run everything**

Run: `go vet ./... && go test ./... && go test -tags e2e ./test/e2e/ && node --test web/static/js/*.test.js`
Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add web README.md CLAUDE.md docs-site/src/content/docs
git commit -m "Settings page: daily digest block; docs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## After the last task

Push `feat/daily-digest`, open a PR closing #26 (what changed, what was verified — "checked with fake notifiers and a local build, not against real ntfy/Telegram" unless a real send was done), wait for CI (incl. `-race` and the screenshots job), squash-merge on green.
