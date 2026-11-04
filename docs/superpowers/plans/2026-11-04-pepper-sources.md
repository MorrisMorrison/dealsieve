# Pepper sources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user turn on hotukdeals, dealabs, preisjaeger, chollometro and pepper.pl next to mydealz on the Settings page (issue #40).

**Architecture:** One generic `internal/source/pepper` adapter with a site table replaces `internal/source/mydealz`. A `sources` block in the settings document picks the enabled sites; the poller runs one loop per (source, feed) and starts/stops loops when settings are applied. Store queries take a source filter, recurrence is per source, the rules engine knows per-source temperature minimums, and the page shows a site label and a source filter when more than one source is on.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `encoding/xml`, `log/slog`, `database/sql`), modernc SQLite, vanilla HTML/CSS/ES modules, `node --test`.

Spec: `docs/superpowers/specs/2026-11-04-pepper-sources-design.md`.

## Global Constraints

- Source names, in this order everywhere: `mydealz`, `hotukdeals`, `dealabs`, `preisjaeger`, `chollometro`, `pepperpl`. Display label: `pepperpl` → `pepper.pl`, every other name is its own label.
- Site table (base URL, new-deals path; trending is `/rss/trending` for all): mydealz `https://www.mydealz.de` `/rss/alles`; hotukdeals `https://www.hotukdeals.com` `/rss/new`; dealabs `https://www.dealabs.com` `/rss/tous`; preisjaeger `https://www.preisjaeger.at` `/rss/alle`; chollometro `https://www.chollometro.com` `/rss/nuevos`; pepperpl `https://www.pepper.pl` `/rss/wszystkie`.
- Deal IDs are `<source>:<thread id>`; `Deal.Source` is the source name.
- Defaults: mydealz enabled, every other source disabled, no source `min_temperature`.
- Only direct dependency stays `modernc.org/sqlite`. No new dependency. CGO-free.
- Fixtures are made up only (`Example Shop`, `static.example.test`); never copy feed content.
- Public repo: no personal domains, hosts, local paths (never this machine's Go path) or chat IDs in code, fixtures, docs or commits. Never describe dealsieve as an alternative to any site: mydealz is "the first source".
- Errors wrapped with `%w`; logs are a constant message plus key/value attrs; comments sparse and explain why.
- Front-end: DOM via `textContent`/attributes, never `innerHTML` with data.
- Gates (run from the repo root): `gofmt -l .` prints nothing; `go vet ./...`; `go test ./...`; `go test -tags e2e ./test/e2e/`; `node --test web/static/js/*.test.js`. (`-race` runs in CI.)
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## File map

| File | Change |
|---|---|
| `internal/source/mydealz/` → `internal/source/pepper/` | `git mv`; site table, `Parse(r, site)`, `Client.Site` |
| `internal/source/pepper/testdata/` | existing `trending.xml`/`new.xml` stay (mydealz); new `<site>-trending.xml` + `<site>-new.xml` for the five new sites |
| `internal/config/config.go` | `SourceURLs map[string]string` replaces `MydealzURL` |
| `internal/settings/settings.go`, `sources.go` (new), `summary.go` | `Sources`, names, labels, validation, summaries |
| `internal/fingerprint/fingerprint.go` | new stopwords; `RekeyOld` drops stopwords |
| `internal/rules/rules.go` | `Deal.Source`, per-source minimums |
| `internal/store/deals.go`, `verdicts.go`, `settings.go`, `digest.go` | source-scoped recurrence; source filters |
| `internal/poller/poller.go` | loops per (source, feed), `SetSources` |
| `internal/pipeline/pipeline.go` | enabled sources, retry skip, label in messages |
| `internal/notify/notify.go` | `DealMessage` gets the source label |
| `internal/digest/digest.go`, `runner.go` | source filter, label per line |
| `internal/api/api.go`, `settings.go` | `?source=`, `source`/`source_label`, status `sources`, settings `sites`, categories |
| `cmd/dealsieve/main.go` | one client per site, applier that also calls `SetSources` |
| `web/index.html`, `web/settings.html`, `web/static/js/{api,main,render,settings}.js`, `web/static/css/*` | label, filter, Sources card |
| `test/e2e/e2e_test.go` | a second site |
| `README.md`, `CLAUDE.md`, `.claude/skills/add-source/SKILL.md`, `docs-site/src/content/docs/*` | docs |

---

### Task 1: Pepper adapter and per-site base URLs

**Files:**
- Move: `internal/source/mydealz/` → `internal/source/pepper/` (`git mv internal/source/mydealz internal/source/pepper`, then `git mv internal/source/pepper/mydealz.go internal/source/pepper/pepper.go` and `mydealz_test.go` → `pepper_test.go`)
- Create: `internal/source/pepper/testdata/{hotukdeals,dealabs,preisjaeger,chollometro,pepperpl}-{trending,new}.xml`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`, `cmd/dealsieve/main.go`, any import of `internal/source/mydealz` (grep for it, including `test/e2e` and `screenshots/` docs)

**Interfaces:**
- Produces: package `pepper` with
  - `const TrendingPath = "/rss/trending"`
  - `type Site struct { Name, BaseURL, NewPath string }`
  - `func Sites() []Site` (copy of the table, in the global order), `func Lookup(name string) (Site, bool)`
  - `func Parse(r io.Reader, site string) ([]source.Deal, []error, error)`
  - `type Client struct { Site Site; BaseURL string; UserAgent string; HTTP *http.Client; Log *slog.Logger }` implementing `source.Fetcher`; empty `BaseURL` means `Site.BaseURL`
  - `type StatusError struct{ Code int }` (unchanged)
- Produces: `config.Env.SourceURLs map[string]string` (every site name → base URL, overrides from `DEALSIEVE_<UPPER(NAME)>_URL`). `config.Env.MydealzURL` is removed.

- [ ] **Step 1: Move the package**

```bash
git mv internal/source/mydealz internal/source/pepper
git mv internal/source/pepper/mydealz.go internal/source/pepper/pepper.go
git mv internal/source/pepper/mydealz_test.go internal/source/pepper/pepper_test.go
```
Change `package mydealz` to `package pepper` in both files and the package comment to `// Package pepper reads deals from the RSS feeds of mydealz and the other sites of the Pepper network, which share one format.`

- [ ] **Step 2: Write the fixtures for the five new sites**

Each new site gets two hand-made files in the mydealz fixture's structure. Use only made-up deals. Thread ids per site: hotukdeals 5000001…, dealabs 3000001…, preisjaeger 300001…, chollometro 2000001…, pepperpl 1000001…. Links use the site's own domain and its deal path (`/deals/` for hotukdeals, preisjaeger; `/bons-plans/` for dealabs; `/ofertas/` for chollometro; `/promocje/` for pepper.pl). Prices in each site's format. Example, `testdata/hotukdeals-trending.xml`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:media="http://search.yahoo.com/mrss/" xmlns:pepper="http://www.pepper.com/rss" version="2.0">
<channel>
<title>hotukdeals</title>
<link>https://www.hotukdeals.com</link>
<item>
<category><![CDATA[Gaming]]></category>
<pepper:merchant name="Example Shop" price="£6.79"/>
<media:content medium="image" url="https://static.example.test/5000001.jpg" width="100" height="100"/>
<title><![CDATA[312° - Example Wireless Controller for the Example Console]]></title>
<link>https://www.hotukdeals.com/deals/example-wireless-controller-5000001</link>
</item>
<item>
<category><![CDATA[Home & Living]]></category>
<pepper:merchant name="Example Market"/>
<title><![CDATA[88° - Free Example Tea Sample]]></title>
<link>https://www.hotukdeals.com/deals/free-example-tea-sample-5000002</link>
</item>
</channel>
</rss>
```

`hotukdeals-new.xml`: same shape, two items without the `NNN° - ` prefix (ids 5000003, 5000004, prices `£40`, `£1.50`).
`dealabs-trending.xml`: `1 504,89€` (ASCII space) and `66,99€`, categories `High-Tech`, `Maison & Habitat`, French made-up titles such as `Example Ordinateur portable 16" pour le jeu`. `dealabs-new.xml`: two items without temperature.
`preisjaeger-trending.xml` / `-new.xml`: `41,37€`, `10€`, categories `Elektronik`, `Lebensmittel & Haushalt`.
`chollometro-trending.xml` / `-new.xml`: `16,80€`, `279€`, categories `Electrónica`, `Hogar, vivienda y oficina`, Spanish made-up titles such as `Example Plancha de vapor con depósito`.
`pepperpl-trending.xml` / `-new.xml`: `309,03zł`, `118 500zł`, categories `Elektronika`, `Dom i mieszkanie`, Polish made-up titles such as `Example Odkurzacz bezprzewodowy dla domu`.

- [ ] **Step 3: Write the failing tests**

In `pepper_test.go`: change `parseFile` to `parseFile(t, name, site string)` calling `Parse(f, site)`; existing mydealz tests pass `"mydealz"`. The existing `Fetch` tests construct `Client{Site: mustLookup(t, "mydealz"), BaseURL: srv.URL, ...}`. Add:

```go
func mustLookup(t *testing.T, name string) Site {
	t.Helper()
	s, ok := Lookup(name)
	if !ok {
		t.Fatalf("no site %q", name)
	}
	return s
}

func TestSitesTable(t *testing.T) {
	want := map[string][2]string{
		"mydealz":     {"https://www.mydealz.de", "/rss/alles"},
		"hotukdeals":  {"https://www.hotukdeals.com", "/rss/new"},
		"dealabs":     {"https://www.dealabs.com", "/rss/tous"},
		"preisjaeger": {"https://www.preisjaeger.at", "/rss/alle"},
		"chollometro": {"https://www.chollometro.com", "/rss/nuevos"},
		"pepperpl":    {"https://www.pepper.pl", "/rss/wszystkie"},
	}
	var names []string
	for _, s := range Sites() {
		names = append(names, s.Name)
		if w := want[s.Name]; w != [2]string{s.BaseURL, s.NewPath} {
			t.Errorf("%s = %+v, want %v", s.Name, s, w)
		}
	}
	if !slices.Equal(names, settings.SourceNames) {
		t.Errorf("site names %v, settings.SourceNames %v", names, settings.SourceNames)
	}
}

func TestParseEverySite(t *testing.T) {
	cases := []struct {
		site, file, firstID, firstPrice string
		temp                            bool
	}{
		{"hotukdeals", "hotukdeals-trending.xml", "hotukdeals:5000001", "£6.79", true},
		{"hotukdeals", "hotukdeals-new.xml", "hotukdeals:5000003", "£40", false},
		{"dealabs", "dealabs-trending.xml", "dealabs:3000001", "1 504,89€", true},
		// one trending and one new row per remaining site, with the ids and prices from the fixtures
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			deals, itemErrs := parseFile(t, c.file, c.site)
			if len(itemErrs) != 0 || len(deals) != 2 {
				t.Fatalf("got %d deals, item errors %v; want 2 and none", len(deals), itemErrs)
			}
			d := deals[0]
			if d.ID != c.firstID || d.Source != c.site || d.Price != c.firstPrice || (d.Temperature != nil) != c.temp {
				t.Errorf("deal 0 = %+v (temperature %v)", d, d.Temperature)
			}
		})
	}
}

func TestFetchUsesEachSitesPaths(t *testing.T) {
	for _, site := range Sites() {
		t.Run(site.Name, func(t *testing.T) {
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if ua := r.Header.Get("User-Agent"); ua != "dealsieve-test" {
					t.Errorf("User-Agent = %q", ua)
				}
				w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`))
			}))
			defer srv.Close()
			c := &Client{Site: site, BaseURL: srv.URL, UserAgent: "dealsieve-test", HTTP: srv.Client(), Log: slog.New(slog.DiscardHandler)}
			for _, feed := range []source.Feed{source.FeedTrending, source.FeedNew} {
				if _, err := c.Fetch(context.Background(), feed); err != nil {
					t.Fatal(err)
				}
			}
			if want := []string{TrendingPath, site.NewPath}; !slices.Equal(paths, want) {
				t.Errorf("paths = %v, want %v", paths, want)
			}
		})
	}
}
```
Fill in the remaining `TestParseEverySite` rows (all 10 new fixture files) explicitly; don't leave the comment.

In `config_test.go` add:

```go
func TestSourceURLs(t *testing.T) {
	env := map[string]string{"DEALSIEVE_HOTUKDEALS_URL": "http://127.0.0.1:9999"}
	e, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if got := e.SourceURLs["hotukdeals"]; got != "http://127.0.0.1:9999" {
		t.Errorf("hotukdeals = %q", got)
	}
	if got := e.SourceURLs["pepperpl"]; got != "https://www.pepper.pl" {
		t.Errorf("pepperpl default = %q", got)
	}
	if len(e.SourceURLs) != 6 {
		t.Errorf("got %d source URLs, want 6", len(e.SourceURLs))
	}
	env["DEALSIEVE_DEALABS_URL"] = "ftp://example.test"
	if _, err := FromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "DEALSIEVE_DEALABS_URL") {
		t.Errorf("bad URL error = %v", err)
	}
}
```
Replace any existing `MydealzURL` assertions with `SourceURLs["mydealz"]`.

Note: `TestSitesTable` imports `internal/settings` for `SourceNames`, which Task 2 adds. In this task, add the minimal declaration to `internal/settings/sources.go` now so the test compiles:

```go
package settings

// SourceNames are the known deal sources, in display order. The pepper
// package's site table is tested to list the same names.
var SourceNames = []string{"mydealz", "hotukdeals", "dealabs", "preisjaeger", "chollometro", "pepperpl"}
```

- [ ] **Step 4: Run the tests and watch them fail**

Run: `go test ./internal/source/... ./internal/config/`
Expected: compile errors (`Lookup`, `Sites`, `TrendingPath`, `SourceURLs` undefined).

- [ ] **Step 5: Implement**

`pepper.go` (replace the `Name`/`feedPaths` part; the parser body stays as it is):

```go
const TrendingPath = "/rss/trending"

// Site is one Pepper site. Every site serves its trending feed at
// TrendingPath; NewPath is where its /rss/new redirects to, so dealsieve
// never relies on the redirect.
type Site struct {
	Name    string
	BaseURL string
	NewPath string
}

var sites = []Site{
	{"mydealz", "https://www.mydealz.de", "/rss/alles"},
	{"hotukdeals", "https://www.hotukdeals.com", "/rss/new"},
	{"dealabs", "https://www.dealabs.com", "/rss/tous"},
	{"preisjaeger", "https://www.preisjaeger.at", "/rss/alle"},
	{"chollometro", "https://www.chollometro.com", "/rss/nuevos"},
	{"pepperpl", "https://www.pepper.pl", "/rss/wszystkie"},
}

func Sites() []Site { return slices.Clone(sites) }

func Lookup(name string) (Site, bool) {
	i := slices.IndexFunc(sites, func(s Site) bool { return s.Name == name })
	if i < 0 {
		return Site{}, false
	}
	return sites[i], true
}
```
`Parse(r io.Reader, site string)` passes `site` to `toDeal(it, site)`, which sets `ID: site + ":" + m[1], Source: site`. `Client` gets a `Site Site` field; `Fetch` picks the path:

```go
func (c *Client) path(feed source.Feed) (string, error) {
	switch feed {
	case source.FeedTrending:
		return TrendingPath, nil
	case source.FeedNew:
		return c.Site.NewPath, nil
	}
	return "", fmt.Errorf("unknown feed %q", feed)
}
```
and the base URL is `c.BaseURL`, or `c.Site.BaseURL` when empty. `Parse(..., c.Site.Name)`. Add `"site", c.Site.Name` to the skip-item warning.

`config.go`: remove `MydealzURL`; add `SourceURLs map[string]string`, filled in `FromEnv`:

```go
	e.SourceURLs = make(map[string]string, len(pepper.Sites()))
	for _, s := range pepper.Sites() {
		key := "DEALSIEVE_" + strings.ToUpper(s.Name) + "_URL"
		v := getenv(key)
		if v == "" {
			e.SourceURLs[s.Name] = s.BaseURL
			continue
		}
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s: must be an absolute http(s) URL, got %q", key, v))
			continue
		}
		e.SourceURLs[s.Name] = strings.TrimRight(v, "/")
	}
```

`main.go` (temporary until Task 6): build the mydealz client from the table:

```go
	site, _ := pepper.Lookup("mydealz")
	src := &pepper.Client{Site: site, BaseURL: env.SourceURLs[site.Name], UserAgent: ..., HTTP: feedClient, Log: log}
```

- [ ] **Step 6: Run the gates**

Run: `gofmt -l . && go vet ./... && go test ./... && go test -tags e2e ./test/e2e/`
Expected: all PASS, gofmt prints nothing.

- [ ] **Step 7: Commit**

```bash
git add -A internal/source internal/config internal/settings/sources.go cmd/dealsieve
git commit -m "Pepper adapter: one parser and client for every Pepper site"
```

---

### Task 2: Sources in the settings document

**Files:**
- Modify: `internal/settings/sources.go` (from Task 1), `internal/settings/settings.go`, `internal/settings/summary.go`
- Test: `internal/settings/settings_test.go`, `internal/settings/summary_test.go`

**Interfaces:**
- Consumes: `settings.SourceNames` (Task 1).
- Produces:
  - `type Source struct { Enabled bool \`json:"enabled"\`; MinTemperature *int \`json:"min_temperature,omitempty"\` }`
  - `Settings.Sources map[string]Source \`json:"sources"\``
  - `func SourceLabel(name string) string` (`pepperpl` → `pepper.pl`, others unchanged)
  - `func (s Settings) EnabledSources() []string` (in `SourceNames` order)
  - `func (s Settings) SourceMinimums() map[string]int` (only sources with a value, enabled or not)

- [ ] **Step 1: Write the failing tests** (`settings_test.go`)

```go
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
```

`summary_test.go`:

```go
func TestSummarizeSources(t *testing.T) {
	prev := Defaults()
	next := Defaults()
	next.Sources = maps.Clone(prev.Sources)
	min150 := 150
	next.Sources["hotukdeals"] = Source{Enabled: true, MinTemperature: &min150}
	got := Summarize(prev, next)
	want := []string{"Sources: mydealz, hotukdeals", "hotukdeals minimum 150°"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	back := Summarize(next, prev)
	if !slices.Contains(back, "hotukdeals minimum: global") || !slices.Contains(back, "Sources: mydealz") {
		t.Errorf("reverse = %q", back)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/settings/`
Expected: compile errors (`Source`, `EnabledSources`, …).

- [ ] **Step 3: Implement**

`sources.go`:

```go
type Source struct {
	Enabled        bool `json:"enabled"`
	MinTemperature *int `json:"min_temperature,omitempty"` // nil: the global minimum
}

var sourceLabels = map[string]string{"pepperpl": "pepper.pl"}

// SourceLabel is how the page and messages name a source.
func SourceLabel(name string) string {
	if l, ok := sourceLabels[name]; ok {
		return l
	}
	return name
}

func defaultSources() map[string]Source {
	out := make(map[string]Source, len(SourceNames))
	for _, n := range SourceNames {
		out[n] = Source{Enabled: n == "mydealz"}
	}
	return out
}

// EnabledSources lists the enabled sources in SourceNames order.
func (s Settings) EnabledSources() []string {
	var out []string
	for _, n := range SourceNames {
		if s.Sources[n].Enabled {
			out = append(out, n)
		}
	}
	return out
}

// SourceMinimums are the sources' own temperature minimums.
func (s Settings) SourceMinimums() map[string]int {
	out := map[string]int{}
	for n, src := range s.Sources {
		if src.MinTemperature != nil {
			out[n] = *src.MinTemperature
		}
	}
	return out
}
```

`settings.go`: add the `Sources` field after `Digest`; `Defaults()` sets `Sources: defaultSources()`. `Decode` already starts from the defaults; `encoding/json` decodes each map value into a fresh zero `Source`, so a stored `{"enabled": false}` stays off (the test pins this). `Normalize`:

```go
	out.Sources = defaultSourcesOff()
	for name, src := range s.Sources {
		out.Sources[strings.ToLower(strings.TrimSpace(name))] = src
	}
```
with `defaultSourcesOff()` returning every known name as `Source{}`; keep unknown names so `Validate` reports them. `Validate`:

```go
	known := map[string]bool{}
	for _, n := range SourceNames {
		known[n] = true
	}
	for name, src := range s.Sources {
		if !known[name] {
			errs["sources."+name] = "unknown source"
		}
		if src.MinTemperature != nil && *src.MinTemperature < 0 {
			errs["sources."+name+".min_temperature"] = "must be 0° or more"
		}
	}
	if len(s.EnabledSources()) == 0 {
		errs["sources"] = "turn at least one source on"
	}
```

`summary.go`, after the digest line:

```go
	out = append(out, sourcesDiff(prev, next)...)
```
```go
func sourcesDiff(prev, next Settings) []string {
	var out []string
	if p, n := prev.EnabledSources(), next.EnabledSources(); !slices.Equal(p, n) {
		labels := make([]string, len(n))
		for i, name := range n {
			labels[i] = SourceLabel(name)
		}
		out = append(out, "Sources: "+strings.Join(labels, ", "))
	}
	for _, name := range SourceNames {
		p, n := prev.Sources[name].MinTemperature, next.Sources[name].MinTemperature
		switch {
		case n != nil && (p == nil || *p != *n):
			out = append(out, fmt.Sprintf("%s minimum %d°", SourceLabel(name), *n))
		case n == nil && p != nil:
			out = append(out, SourceLabel(name)+" minimum: global")
		}
	}
	return out
}
```
Check `TestSummarizeSources`'s expected order against where you append; keep the test and the code consistent.

- [ ] **Step 4: Run the gates** (`gofmt -l .`, `go vet ./...`, `go test ./...`). Existing settings tests that compare whole documents may need the new default `sources` key; update them to include it rather than dropping the comparison.

- [ ] **Step 5: Commit** — `git commit -m "Settings: a sources block, mydealz on by default"`

---

### Task 3: Stopwords for French, Spanish, Polish and English titles

**Files:**
- Modify: `internal/fingerprint/fingerprint.go`
- Test: `internal/fingerprint/fingerprint_test.go`

**Interfaces:**
- Produces: no signature change. `CleanTitle` drops more words; `RekeyOld(key)` also drops stopwords from a key's words.

- [ ] **Step 1: Write the failing tests**

```go
func TestCleanTitleDropsOtherLanguagesStopwords(t *testing.T) {
	cases := map[string]string{
		"Example Plancha de vapor con depósito para el hogar": "example plancha vapor depósito hogar",
		"Example Ordinateur portable pour le jeu avec souris":   "example ordinateur portable jeu souris",
		"Example Odkurzacz bezprzewodowy dla domu na raty":      "example odkurzacz bezprzewodowy domu raty",
		"Example Socks by Example Brand, 20% off":               "example socks example brand",
	}
	for in, want := range cases {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRekeyOldDropsStopwords(t *testing.T) {
	if got := RekeyOld("example shop|de echo la para"); got != "example shop|echo" {
		t.Errorf("RekeyOld = %q", got)
	}
	// Idempotent on a current key.
	k := Key("Example Shop", CleanTitle("Example Echo Dot"))
	if RekeyOld(k) != k {
		t.Errorf("RekeyOld changed a current key %q", k)
	}
}
```

- [ ] **Step 2: Run and watch them fail** — `go test ./internal/fingerprint/`

- [ ] **Step 3: Implement**

Add to `stopwords` (keep the existing entries; group with comments):

```go
	// French
	"le": true, "la": true, "les": true, "du": true, "et": true, "pour": true, "avec": true,
	"au": true, "aux": true, "sur": true, "par": true, "chez": true, "dans": true, "une": true,
	// Spanish
	"el": true, "los": true, "las": true, "del": true, "con": true, "por": true, "para": true, "al": true,
	// French and Spanish
	"de": true, "en": true,
	// Polish
	"na": true, "do": true, "dla": true, "od": true, "ze": true, "za": true, "po": true,
	"oraz": true, "lub": true, "przy": true,
```
and to the English group: `"by": true, "via": true, "per": true, "off": true,`. Update the package/`CleanTitle` comment ("German, English, French, Spanish and Polish stopwords"). In `RekeyOld`, keep a word only if `!isQuantity(w) && !stopwords[w]`, and change its comment: it converts keys made by older rules (digit tokens dropped, fewer stopwords) to the current ones.

- [ ] **Step 4: Gates**; existing fingerprint and store `Rekey` tests must still pass unchanged.

- [ ] **Step 5: Commit** — `git commit -m "Fingerprints: stopwords for French, Spanish, Polish and English titles"`

---

### Task 4: Per-source temperature minimum in the rules

**Files:**
- Modify: `internal/rules/rules.go`, `internal/pipeline/pipeline.go` (two call sites)
- Test: `internal/rules/rules_test.go`

**Interfaces:**
- Consumes: `settings.Settings.SourceMinimums()` (Task 2).
- Produces: `rules.Deal.Source string`; `rules.New(r settings.Rules, watchlist []string, sourceMins map[string]int) *Engine`.

- [ ] **Step 1: Write the failing table test**

```go
func TestTemperatureThresholdPrecedence(t *testing.T) {
	r := settings.Defaults().Rules // min 100
	r.CategoryOverrides = map[string]int{"Gaming": 50}
	e := New(r, nil, map[string]int{"hotukdeals": 150})
	temp := func(n int) *int { return &n }
	cases := []struct {
		name, source, category string
		temperature            int
		decision               Decision
		reason                 string
	}{
		{"global decides", "mydealz", "", 90, Block, "too cold: 90° < 100°"},
		{"source decides block", "hotukdeals", "", 120, Block, "too cold: 120° < 150° (hotukdeals minimum)"},
		{"source decides pass", "hotukdeals", "", 210, Pass, "hot: 210° (hotukdeals minimum 150°)"},
		{"category beats source", "hotukdeals", "Gaming", 60, Pass, "hot: 60° (minimum 50°)"},
		{"global pass", "mydealz", "", 120, Pass, "hot: 120° (minimum 100°)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := e.Evaluate(Deal{Title: "Example", Source: c.source, Category: c.category, Fingerprint: "x",
				Temperature: temp(c.temperature), InTrending: true}, Facts{})
			if res.Decision != c.decision || res.Reasons[0] != c.reason {
				t.Errorf("got %s %q, want %s %q", res.Decision, res.Reasons, c.decision, c.reason)
			}
		})
	}
	// A watchlist hit below a source minimum names it too.
	w := New(r, []string{"example"}, map[string]int{"hotukdeals": 150})
	res := w.Evaluate(Deal{Title: "Example", Source: "hotukdeals", Fingerprint: "x", Temperature: temp(120), InTrending: true}, Facts{})
	if !slices.Contains(res.Reasons, "below the hotukdeals minimum of 150°, kept for your watchlist") {
		t.Errorf("watchlist reasons = %q", res.Reasons)
	}
}
```
Every existing `New(r, w)` call in tests becomes `New(r, w, nil)`.

- [ ] **Step 2: Run and watch it fail** — `go test ./internal/rules/`

- [ ] **Step 3: Implement**

Engine gets `sourceMins map[string]int` (copied in `New`). Replace `minTemperatureFor`:

```go
// minTemperatureFor returns the minimum for a deal and, when the source's own
// minimum decided, the source's name: a category override wins, then the
// source's minimum, then the global one.
func (e *Engine) minTemperatureFor(category, src string) (int, string) {
	if v, ok := e.overrides[category]; ok {
		return v, ""
	}
	if v, ok := e.sourceMins[src]; ok {
		return v, src
	}
	return e.minTemperature, ""
}
```
In `blockReason`:

```go
		if minimum, by := e.minTemperatureFor(category, d.Source); *d.Temperature < minimum {
			if by != "" {
				return fmt.Sprintf("too cold: %d° < %d° (%s minimum)", *d.Temperature, minimum, settings.SourceLabel(by)), true
			}
			return fmt.Sprintf("too cold: %d° < %d°", *d.Temperature, minimum), true
		}
```
In `passReasons`, `hot: %d° (%s minimum %d°)` with a source, `hot: %d° (minimum %d°)` without; `below the %s minimum of %d°, kept for your watchlist` with a source, the existing text without. `pipeline.go`: `rules.New(s.Rules, s.Watchlist, s.SourceMinimums())` in `ApplySettings`, and `Source: rec.Source` in `evaluate`'s `rules.Deal`. Grep for other `rules.New(` calls (tests in pipeline/api/settingsvc) and add `nil`.

- [ ] **Step 4: Gates.** Rule order in `blockReason` is unchanged.

- [ ] **Step 5: Commit** — `git commit -m "Rules: a source's own temperature minimum"`

---

### Task 5: Source-aware store queries

**Files:**
- Modify: `internal/store/deals.go`, `internal/store/verdicts.go`, `internal/store/settings.go`, `internal/store/digest.go`; callers in `internal/pipeline/pipeline.go`, `internal/api/api.go`, `internal/api/settings.go`, `internal/digest/runner.go`
- Test: `internal/store/*_test.go`

**Interfaces:**
- Produces (nil `sources` means no filter; callers in later tasks pass the enabled sources):
  - `CountRecurrence(ctx, source, fingerprint, id string, firstSeen time.Time, window time.Duration) (int, time.Time, error)` — only deals of `source`
  - `ListDeals(ctx, view string, sources []string, since time.Time, limit int) ([]Listed, error)`
  - `CountViews(ctx, sources []string, since time.Time) (map[string]int, error)`
  - `Categories(ctx, sources []string) ([]string, error)`
  - `DigestDeals(ctx, channel string, sources []string, since time.Time, top int) (hits, others []DealRecord, err error)`

- [ ] **Step 1: Write the failing tests**

Use the existing store test helpers (temp SQLite, `UpsertDeal`, `SaveVerdict`). Add:

```go
func TestCountRecurrenceIsPerSource(t *testing.T) {
	st := openTest(t) // the helper the other store tests use
	ctx := context.Background()
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	add := func(id, src string, at time.Time) DealRecord {
		rec, err := st.UpsertDeal(ctx, source.Deal{ID: id, Source: src, URL: "https://example.test/" + id, Title: "Example Echo"},
			source.FeedTrending, "example echo", "example shop|echo", at)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	add("mydealz:1", "mydealz", t0)
	add("preisjaeger:1", "preisjaeger", t0.Add(time.Hour))
	last := add("mydealz:2", "mydealz", t0.Add(2*time.Hour))
	n, _, err := st.CountRecurrence(ctx, "mydealz", last.Fingerprint, last.ID, last.FirstSeen, 90*24*time.Hour)
	if err != nil || n != 1 {
		t.Errorf("mydealz recurrence = %d, %v; want 1 (the preisjaeger deal doesn't count)", n, err)
	}
}

func TestListingsFilterBySource(t *testing.T) {
	// Two passed deals, one per source, in category "Gaming" and "Elektronik".
	// ListDeals/CountViews/Categories with []string{"hotukdeals"} see only the hotukdeals one;
	// with nil they see both. DigestDeals with []string{"mydealz"} lists only the mydealz one.
}
```
Write the second test out fully with the helpers (upsert two deals with different `Source`, `SaveVerdict` pass for both, then assert each of the four functions with `nil`, with one source, and the counts map).

- [ ] **Step 2: Run and watch them fail** — `go test ./internal/store/`

- [ ] **Step 3: Implement**

A helper in `verdicts.go`:

```go
// sourceFilter limits a query on deals d to sources; nil means every source.
func sourceFilter(sources []string) (string, []any) {
	if sources == nil {
		return "", nil
	}
	if len(sources) == 0 {
		return " AND 0", nil
	}
	args := make([]any, len(sources))
	for i, s := range sources {
		args[i] = s
	}
	return " AND d.source IN (?" + strings.Repeat(", ?", len(sources)-1) + ")", args
}
```
`CountRecurrence` adds `AND source = ?` with the source as the first argument after the fingerprint. `ListDeals`/`CountViews` append the filter after `d.last_seen >= ?` (arguments in order: since, filter args, then limit). `Categories` becomes `SELECT DISTINCT d.category FROM deals d WHERE d.category <> ''` + filter. `DigestDeals`/`digestQuery` take the filter fragment and arguments. Update the callers to compile: pipeline `evaluate` passes `rec.Source`; api and digest pass `nil` for now (Tasks 7 and 8 pass the enabled sources). Update comments on each function: "sources nil means every source".

- [ ] **Step 4: Gates.**

- [ ] **Step 5: Commit** — `git commit -m "Store: per-source recurrence and source filters"`

---

### Task 6: Poller loops per source, applied live; wiring

**Files:**
- Modify: `internal/poller/poller.go`, `cmd/dealsieve/main.go`
- Test: `internal/poller/poller_test.go`

**Interfaces:**
- Consumes: `pepper.Sites()`, `pepper.Client` (Task 1); `config.Env.SourceURLs` (Task 1); `settings.Settings.EnabledSources()` (Task 2).
- Produces:
  - `poller.New(fetchers map[string]source.Fetcher, ing Ingester, intervals map[source.Feed]time.Duration, log *slog.Logger) *Poller`
  - `(*Poller).SetSources(names []string)` — safe before and during `Run`; names without a fetcher are logged and skipped
  - `(*Poller).Run(ctx)` — starts the loops for the current sources, returns after ctx ends and every loop stopped
  - `(*Poller).PollOnce(ctx, src string, feed source.Feed) int`
  - `poller.Status` gains `Source string \`json:"source"\`` (the name) and `Label string \`json:"label"\`` (`settings.SourceLabel`); `Status()` returns one row per running loop, ordered by the order of the names last given to `SetSources`, then feed

- [ ] **Step 1: Write the failing tests**

Keep the existing `nextDelay` tests. Replace the fake fetcher with one that records `(source, feed)` calls per source name. Add:

```go
func TestSetSourcesStartsAndStopsLoops(t *testing.T) {
	calls := make(chan string, 100)
	fetchers := map[string]source.Fetcher{}
	for _, n := range []string{"mydealz", "hotukdeals"} {
		fetchers[n] = fetcherFunc(func(ctx context.Context, feed source.Feed) ([]source.Deal, error) {
			calls <- n + "/" + string(feed)
			return nil, nil
		})
	}
	p := New(fetchers, nopIngester{}, map[source.Feed]time.Duration{source.FeedTrending: time.Hour}, slog.New(slog.DiscardHandler))
	p.SetSources([]string{"mydealz"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	if got := <-calls; got != "mydealz/trending" {
		t.Fatalf("first poll = %q", got)
	}
	p.SetSources([]string{"hotukdeals", "example"}) // unknown names are skipped
	if got := <-calls; got != "hotukdeals/trending" {
		t.Fatalf("after switch = %q", got)
	}
	st := p.Status()
	if len(st) != 1 || st[0].Source != "hotukdeals" || st[0].Feed != "trending" {
		t.Errorf("status = %+v", st)
	}
	cancel()
	<-done
}
```
(with `fetcherFunc` and `nopIngester` as small test types). Adapt the existing `PollOnce` tests to `PollOnce(ctx, "mydealz", feed)` after `SetSources([]string{"mydealz"})`. Add a test that `PollOnce` for a source that was turned off in the meantime returns without panicking.

- [ ] **Step 2: Run and watch them fail** — `go test ./internal/poller/`

- [ ] **Step 3: Implement**

```go
type loopKey struct {
	source string
	feed   source.Feed
}

type Poller struct {
	fetchers  map[string]source.Fetcher
	ingest    Ingester
	intervals map[source.Feed]time.Duration
	log       *slog.Logger
	now       func() time.Time

	mu      sync.Mutex
	order   []string                      // enabled sources, as last set
	running map[string]context.CancelFunc // per source
	state   map[loopKey]*feedState
	ctx     context.Context // Run's; nil before Run
	closed  bool            // Run is waiting for the loops to end
	wg      sync.WaitGroup
}
```
`feedState` gains `started time.Time` (staleness counts from the loop's start, replacing `Poller.started`). `SetSources` stores the known names in `order` (logs `"unknown source"` for others) and, when `ctx != nil && !closed`, calls `reconcile()`. `reconcile` (mutex held) cancels and forgets the loops and state of sources not in `order`, then for each source in `order` without a running loop creates `context.WithCancel(p.ctx)`, a `feedState{started: p.now()}` per feed and starts `p.wg.Go(func() { p.loop(ctx, src, feed, interval) })`. `loop` is today's `Run` body for one feed. `Run`:

```go
func (p *Poller) Run(ctx context.Context) {
	p.mu.Lock()
	p.ctx, p.running = ctx, map[string]context.CancelFunc{}
	p.reconcile()
	p.mu.Unlock()
	<-ctx.Done()
	p.mu.Lock()
	p.closed = true // no loop may be added once Wait has started
	p.mu.Unlock()
	p.wg.Wait()
}
```
`PollOnce(ctx, src, feed)` fetches with `p.fetchers[src]`, ingests, and then, under the mutex, returns 0 if `p.state[loopKey{src, feed}]` is nil (the source was turned off meanwhile); otherwise it updates the state as today and logs with `"source", src`. `Status()` builds the rows by iterating `order` then the feeds sorted by name.

`main.go`: build the fetchers and create the poller **before** the settings service, so `svc.Load` applies settings to both:

```go
	ua := "dealsieve/" + version.Version + " (+" + repoURL + ")"
	fetchers := map[string]source.Fetcher{}
	for _, site := range pepper.Sites() {
		fetchers[site.Name] = &pepper.Client{Site: site, BaseURL: env.SourceURLs[site.Name], UserAgent: ua, HTTP: feedClient, Log: log}
	}
	pl := poller.New(fetchers, pipe, map[source.Feed]time.Duration{
		source.FeedTrending: env.PollTrending,
		source.FeedNew:      env.PollNew,
	}, log)
	svc := &settingsvc.Service{Store: st, Apply: applyAll{pipe, pl}, Now: time.Now}
```
```go
// applyAll puts settings into effect in the pipeline and the poller.
type applyAll struct {
	*pipeline.Pipeline
	poller *poller.Poller
}

func (a applyAll) ApplySettings(s settings.Settings) {
	a.Pipeline.ApplySettings(s)
	a.poller.SetSources(s.EnabledSources())
}
```
(`RecomputeRecent` comes from the embedded pipeline.) Remove the temporary single client from Task 1.

- [ ] **Step 4: Gates** (incl. e2e: mydealz is on by default, so it still passes).

- [ ] **Step 5: Commit** — `git commit -m "Poller: one loop per source and feed, sources applied live"`

---

### Task 7: Pipeline, notifications and digest know the sources

**Files:**
- Modify: `internal/pipeline/pipeline.go`, `internal/notify/notify.go`, `internal/digest/digest.go`, `internal/digest/runner.go`, `internal/api/settings.go` (only if it calls `DealMessage`)
- Test: `internal/pipeline/pipeline_test.go`, `internal/notify/notify_test.go`, `internal/digest/*_test.go`

**Interfaces:**
- Consumes: `EnabledSources()`, `SourceLabel` (Task 2); `DigestDeals(..., sources, ...)` (Task 5).
- Produces:
  - `notify.DealMessage(source, title, price, merchant string, temperature *int, reasons []string, url, imageURL string) Message` — `source` is a label or ""; when set, `Title` is `source + " · " + title`
  - `digest.Item.Source string` (label or ""); `line` puts `Source + " · "` before the title (outside the link in HTML)

- [ ] **Step 1: Write the failing tests**

- notify: `DealMessage("hotukdeals", "Example", ...)` has title `hotukdeals · Example`; with `""` the title is unchanged.
- digest: `Compose` with an item `Source: "dealabs"` renders `1. dealabs · Example …` in plain text and `1. dealabs · <a href=…>Example</a>` in HTML. A runner test with settings enabling only `mydealz` doesn't list a stored passed `hotukdeals` deal; with both enabled, lines carry labels.
- pipeline: after `ApplySettings` with `mydealz` and `hotukdeals` enabled, a notification for a hotukdeals deal is titled `hotukdeals · …` (use the existing fake-notifier tests); with only mydealz enabled, a mydealz deal's title has no prefix; a pending notification of a deal whose source is off is not retried (seed a failed attempt with `RecordNotification(..., false, ...)`, apply settings without that source, ingest an empty poll, assert the fake notifier got nothing).

- [ ] **Step 2: Run and watch them fail.**

- [ ] **Step 3: Implement**

Pipeline gets `sources map[string]bool` and `multiSource bool`, set in `ApplySettings` from `s.EnabledSources()` under the mutex. `retryPending`, after `GetDeal`: `if p.sources != nil && !p.sources[rec.Source] { continue }` (nil only in tests that never applied settings). `send` passes `p.label(rec.Source)`:

```go
// label names the source in messages, only when several sources are on.
func (p *Pipeline) label(src string) string {
	if !p.multiSource {
		return ""
	}
	return settings.SourceLabel(src)
}
```
Digest runner: read `s := r.Settings()` once per tick/test; pass `s.EnabledSources()` to `DigestDeals`; `items(recs, multi bool)` sets `Source: settings.SourceLabel(rec.Source)` when `len(s.EnabledSources()) > 1`. Thread `sources []string` through `compose`.

- [ ] **Step 4: Gates.**

- [ ] **Step 5: Commit** — `git commit -m "Name the source in notifications and the digest; skip turned-off sources"`

---

### Task 8: API: source filter, labels, sites

**Files:**
- Modify: `internal/api/api.go`, `internal/api/settings.go`
- Test: `internal/api/api_test.go`, `internal/api/settings_test.go`

**Interfaces:**
- Consumes: Task 2, Task 5, Task 6 (`poller.Status` with `Source`/`Label`), `pepper.Sites()`.
- Produces (JSON):
  - `GET /api/deals?view=…&source=<name>`: `source` must be an enabled source, else 400 `{"error":"unknown source"}`; without it, all enabled sources. Each deal has `"source"` and `"source_label"`. `counts` use the same filter.
  - `GET /api/status`: `{"feeds": [...rows with source and label...], "sources": [{"name": "mydealz", "label": "mydealz"}, ...]}` (enabled, in order)
  - `GET /api/categories`: categories of enabled sources
  - `GET /api/settings` (and every settings body built by `settingsBody`): adds `"sites": [{"name","label","domain"}]` for all six sites in order; `domain` is the base URL's host without a leading `www.` (`hotukdeals.com`, `pepper.pl`) taken from `pepper.Sites()` (not `env.SourceURLs`, which tests override)

- [ ] **Step 1: Write the failing tests** (use the existing `httptest` API server helper and seeded store):
  - with settings enabling mydealz + hotukdeals and one passed deal each: `/api/deals` returns both with `source`/`source_label`; `?source=hotukdeals` returns one and counts `{"passed":1,…}`; `?source=dealabs` → 400 `unknown source`.
  - `/api/status` has `sources` with both names.
  - `/api/settings` has six `sites`, the last `{"name":"pepperpl","label":"pepper.pl","domain":"pepper.pl"}`.
  - `/api/categories` with only mydealz enabled leaves out the hotukdeals deal's category.

- [ ] **Step 2: Run and watch them fail.**

- [ ] **Step 3: Implement.** `enabled := s.Settings.Current().Settings.EnabledSources()` at the start of each handler; `slices.Contains(enabled, src)` for the check. `dealJSON` gains `Source string \`json:"source"\`` and `SourceLabel string \`json:"source_label"\``. `status` writes `{"feeds": s.Status(), "sources": labelled(enabled)}` with a small `type sourceJSON struct{ Name, Label string }` (json tags `name`, `label`).

- [ ] **Step 4: Gates.**

- [ ] **Step 5: Commit** — `git commit -m "API: source filter, source labels and the site list"`

---

### Task 9: The page: label, filter, Sources card

**Files:**
- Modify: `web/index.html`, `web/settings.html`, `web/static/js/api.js`, `web/static/js/main.js`, `web/static/js/render.js`, `web/static/js/settings.js` (and `settings-model.js` + its test if the card's form ⇄ document conversion lives there), `web/static/css/*.css`

**Interfaces:**
- Consumes: the JSON from Task 8.
- Produces: `api.getDeals(view, source)` (adds `&source=` only when `source` is set); `api.getStatus()` resolves to `{ feeds, sources }` (update every caller); `renderList(list, deals, selectedId, onSelect, { showSource })`; `renderStatus(node, feeds)` names a stale feed `${f.label} ${feed}` when `f.label` is set.

- [ ] **Step 1: Inbox.** In `index.html` add a `<select id="source-filter" hidden aria-label="Source">` next to the view tabs and a `<span class="source">` in the item template's meta line and in the detail template's price line. In `main.js` keep `state.sources` (from `getStatus`) and `state.source` (`''` = all). When `state.sources.length > 1`, show the select with "All sources" plus one option per source (`textContent = label`, `value = name`); otherwise hide it and reset `state.source = ''`. If the selected source disappears from `state.sources`, reset to `''`. Changing it reloads the list and counts. `render.js` fills `.source` with `deal.source_label` when `showSource`, else leaves it empty and hidden. All via `textContent`.

- [ ] **Step 2: Settings page.** A "Sources" card above the filter rules, built from `sites` in the settings response: per site a row with a checkbox (`label` + a muted `domain` as plain text) and a number input (`min="0"`, `placeholder="global"`, `aria-label="<label> minimum temperature"`). Reading the form: `sources[name] = { enabled, min_temperature }` with `min_temperature` left out when the field is empty. Validation errors keyed `sources`, `sources.<name>`, `sources.<name>.min_temperature` show next to the card/row the way the other cards show theirs. If the form ⇄ document conversion lives in `settings-model.js`, add a `node --test` case: an empty field drops `min_temperature`, `"150"` becomes `150`.

- [ ] **Step 3: CSS.** `.source` is a small muted pill in the row meta and detail, readable in light and dark themes, wrapping fine at 375 px.

- [ ] **Step 4: Run** `node --test web/static/js/*.test.js` and the Go gates (`web/embed_test.go` covers the embed).

- [ ] **Step 5: Commit** — `git commit -m "Page: site label, source filter and the Sources card"`

---

### Task 10: e2e and docs

**Files:**
- Modify: `test/e2e/e2e_test.go`, `README.md`, `CLAUDE.md`, `.claude/skills/add-source/SKILL.md`, `docs-site/src/content/docs/` (configuration page and the settings/filters page; check `astro.config.mjs` if a new page is added)

- [ ] **Step 1: e2e.** The fake feed server also serves `/rss/trending` and `/rss/new` for hotukdeals from `internal/source/pepper/testdata/hotukdeals-*.xml` — start a second `httptest.Server` for it and pass `DEALSIEVE_HOTUKDEALS_URL`. Update the mydealz fixture path to `internal/source/pepper/testdata/`. After the existing assertions, `PUT /api/settings` with `sources.hotukdeals.enabled = true` (keep the version handshake used by the test), then poll `/api/deals?view=passed&source=hotukdeals` (with the test's existing wait helper) until a deal with `source == "hotukdeals"` appears, and assert every returned deal has that source. Make sure the hotukdeals trending fixture has at least one deal that passes with the e2e seed settings (temperature ≥ the seed minimum).

- [ ] **Step 2: Docs.**
  - README: "Sources" section (mydealz is the first source; hotukdeals, dealabs, preisjaeger, chollometro and pepper.pl can be turned on in Settings; same RSS format; each site polled at the same intervals); env table rows for the five new `DEALSIEVE_<SITE>_URL` (for tests/mirrors, defaults to the site).
  - docs site: the Sources card (on/off, per-source minimum and its precedence), recurrence per source, the label and filter, the env vars on the configuration page.
  - `CLAUDE.md`: layout row `internal/source/` → `source/pepper/` is the RSS adapter for all Pepper sites (site table, fixtures in `testdata/`); "What this is": mydealz is the first source, other Pepper sites can be turned on in Settings; e2e note: serves mydealz and hotukdeals fixtures; Conventions: recurrence counts only the same source; a new Pepper site = a row in `pepper.sites`, a name in `settings.SourceNames` (+ label if needed), fixtures, a docs line.
  - `add-source` skill: a Pepper-network site needs only those steps; a non-Pepper source still follows the existing steps with its own package.

- [ ] **Step 3: Gates**, plus `cd docs-site && npm ci && npm run build` if docs-site pages changed (link check).

- [ ] **Step 4: Commit** — `git commit -m "e2e and docs for Pepper sources"`
