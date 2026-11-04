# Price History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show for each deal whether its price is the lowest dealsieve has seen for the same product ("Lowest seen: 179€ at Example Shop, Aug 2026 · 40 € more than now" / "Best price"), in the detail panel and as a list chip. Information only.

**Architecture:** A new pure package `internal/product` parses prices and decides "same product" from model codes, sizes and title words. The store keeps a `deal_products` row (model-code key, currency, cents) per deal, written by the pipeline on ingest and rebuilt at startup. `/api/deals` fetches candidates by model key and adds `price_history`. The page renders it with pure helpers in `history.js`.

**Tech Stack:** Go 1.26 stdlib, modernc SQLite, vanilla ES modules, `node --test`.

Spec: `docs/superpowers/specs/2026-11-04-price-history-design.md`.

## Global Constraints

- Information only: no change to rules, verdicts, scores, notifications or the digest.
- No new dependencies. Standard library only in Go; no npm packages in `web/`.
- `fingerprint.CleanTitle` and `fingerprint.Key` must not change behaviour (no fingerprint migration). Existing fingerprint tests must pass unchanged.
- New migration file: `internal/store/migrations/004_price_history.sql`. Never edit 001–003.
- The store stays free of domain logic: it never imports `internal/product`; callers pass functions or values.
- Front-end builds the DOM with `textContent` and attributes, never `innerHTML` with data.
- Fixtures and test data are made up (`Example Shop`, `Example Air`, `example.test`). No real feed content, no personal hosts or paths.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Gates before each commit: `gofmt -l .` prints nothing, `go vet ./...`, `go test ./...`; for front-end tasks also `node --test web/static/js/*.test.js`. (`-race` runs only in CI.)

## File structure

| File | Responsibility |
|---|---|
| `internal/product/price.go` (+`_test`) | `ParsePrice` |
| `internal/product/product.go` (+`_test`) | `Info`, `Of`, `Same`, `Index` |
| `internal/product/history.go` (+`_test`) | `Candidate`, `Match`, `History`, `Compare` |
| `internal/fingerprint/fingerprint.go` | exports `Tokens`, `IsQuantity`, `IsUnit` (no behaviour change) |
| `internal/store/migrations/004_price_history.sql` | `deal_products` table |
| `internal/store/products.go` (+`_test`) | `ProductRow`, `SetProduct`, `DeleteProduct`, `ReindexProducts`, `ProductCandidates` |
| `internal/pipeline/pipeline.go` | write the product row on ingest |
| `cmd/dealsieve/main.go` | `ReindexProducts` at startup |
| `internal/api/prices.go`, `api.go` | `price_history` in `/api/deals` |
| `web/static/js/format.js`, `history.js` (+tests), `render.js`, `web/index.html`, `web/static/css/styles.css` | chip and detail section |
| `README.md`, `CLAUDE.md`, `docs-site/.../how-it-works.md`, `docs-site/.../reference/api.md` | docs |

---

### Task 1: `product.ParsePrice`

**Files:**
- Create: `internal/product/price.go`
- Test: `internal/product/price_test.go`

**Interfaces:**
- Produces: `func ParsePrice(text string) (cents int64, currency string, ok bool)` — currency is `EUR`, `GBP`, `USD` or `PLN`.

- [ ] **Step 1: Write the failing test** (`internal/product/price_test.go`)

```go
package product

import "testing"

func TestParsePrice(t *testing.T) {
	tests := []struct {
		in       string
		cents    int64
		currency string
		ok       bool
	}{
		{"9€", 900, "EUR", true},
		{"1.449€", 144900, "EUR", true},
		{"0,01€", 1, "EUR", true},
		{"12,99 €", 1299, "EUR", true},
		{"12,5€", 1250, "EUR", true},
		{"1.000.000€", 100000000, "EUR", true},
		{"ab 5€", 500, "EUR", true},
		{"EUR 15", 1500, "EUR", true},
		{"15 Euro", 1500, "EUR", true},
		{"£12.99", 1299, "GBP", true},
		{"£1,299.99", 129999, "GBP", true},
		{"GBP 3", 300, "GBP", true},
		{"$5", 500, "USD", true},
		{"19,99 zł", 1999, "PLN", true},
		{"1 504,89€", 150489, "EUR", true},        // dealabs: space thousands
		{"118 500zł", 11850000, "PLN", true},      // pepper.pl
		{"1 000 000€", 100000000, "EUR", true},
		{"1 299,00 €", 129900, "EUR", true}, // no-break space
		{"Gratis", 0, "", false},
		{"0€", 0, "", false},
		{"0,00€", 0, "", false},
		{"", 0, "", false},
		{"15", 0, "", false},
		{"€", 0, "", false},
		{"99999999999999€", 0, "", false},
	}
	for _, tt := range tests {
		cents, currency, ok := ParsePrice(tt.in)
		if cents != tt.cents || currency != tt.currency || ok != tt.ok {
			t.Errorf("ParsePrice(%q) = %d, %q, %v; want %d, %q, %v", tt.in, cents, currency, ok, tt.cents, tt.currency, tt.ok)
		}
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/product/`
Expected: FAIL (`undefined: ParsePrice`).

- [ ] **Step 3: Implement** (`internal/product/price.go`)

```go
// Package product recognises the same product across deals and compares
// their prices. It is pure: no I/O.
package product

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	amount = regexp.MustCompile(`\d[\d.,]*`)
	// spaceThousands is a space used as thousands separator ("1 504,89€",
	// "118 500zł"): a digit, a space and exactly three digits.
	spaceThousands = regexp.MustCompile(`(\d)[ \x{00A0}\x{202F}](\d{3})(\D|$)`)
)

// currencies is checked in order; "€" before "eur" so "Euro" texts still
// match, and symbols before codes.
var currencies = []struct{ marker, code string }{
	{"€", "EUR"}, {"£", "GBP"}, {"zł", "PLN"}, {"$", "USD"},
	{"eur", "EUR"}, {"gbp", "GBP"}, {"pln", "PLN"}, {"usd", "USD"},
}

// ParsePrice reads a price as a source shows it ("1.449€", "£1,299.99",
// "12,99 €") into cents and an ISO currency code. A separator followed by
// one or two digits is the decimal separator; any other is a thousands
// separator. Texts without a known currency or amount, and amounts of zero
// (freebies), are not prices.
func ParsePrice(text string) (cents int64, currency string, ok bool) {
	lower := strings.ToLower(text)
	for _, c := range currencies {
		if strings.Contains(lower, c.marker) {
			currency = c.code
			break
		}
	}
	// Matches don't overlap, so "1 000 000" needs more than one pass.
	for prev := ""; prev != lower; {
		prev, lower = lower, spaceThousands.ReplaceAllString(lower, "$1$2$3")
	}
	num := strings.TrimRight(amount.FindString(lower), ".,")
	if currency == "" || num == "" {
		return 0, "", false
	}
	whole, frac := num, ""
	if i := strings.LastIndexAny(num, ".,"); i >= 0 && len(num)-i-1 <= 2 {
		whole, frac = num[:i], num[i+1:]
	}
	whole = strings.NewReplacer(".", "", ",", "").Replace(whole)
	if len(whole) > 12 {
		return 0, "", false
	}
	if len(frac) == 1 {
		frac += "0"
	}
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, "", false
	}
	cents = units * 100
	if frac != "" {
		f, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, "", false
		}
		cents += f
	}
	if cents <= 0 {
		return 0, "", false
	}
	return cents, currency, true
}
```

- [ ] **Step 4: Run it and see it pass**

Run: `go test ./internal/product/`
Expected: `ok`.

- [ ] **Step 5: Gates and commit**

```bash
gofmt -l . ; go vet ./... && go test ./...
git add internal/product/price.go internal/product/price_test.go
git commit -m "feat(product): parse deal prices into cents and currency

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Same product — `Of`, `Same`, `Index`, `Compare`

**Files:**
- Modify: `internal/fingerprint/fingerprint.go` (export helpers; `CleanTitle` uses `Tokens`)
- Create: `internal/product/product.go`, `internal/product/history.go`
- Test: `internal/product/product_test.go`, `internal/product/history_test.go`

**Interfaces:**
- Consumes: `ParsePrice` (Task 1).
- Produces:
  - `fingerprint.Tokens(title string) []string`, `fingerprint.IsQuantity(w string) bool`, `fingerprint.IsUnit(w string) bool`
  - `type Info struct { Models, Sizes, Words []string }` (each sorted, distinct)
  - `func Of(title string) Info`
  - `func Same(a, b Info) bool`
  - `func Index(title, price string) (models, currency string, cents int64, ok bool)` — `models` is `Info.Models` joined by single spaces
  - `type Candidate struct { ID, URL, Title, Merchant, Price, Currency string; Cents int64; FirstSeen time.Time }`
  - `type Match struct` with JSON fields `id, url, title, merchant, price, first_seen`
  - `type History struct` with JSON fields `status, currency, diff_cents, count, lowest, matches`
  - `const StatusBest = "best"`, `StatusAbove = "above"`, `MaxMatches = 10`
  - `func Compare(own Candidate, candidates []Candidate) *History`

- [ ] **Step 1: Export the fingerprint helpers** (no behaviour change). In `internal/fingerprint/fingerprint.go` replace the first statement of `CleanTitle`:

```go
	fields := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
```
with
```go
	fields := Tokens(title)
```
and add after `CleanTitle`:

```go
// Tokens splits a title into its lowercased runs of letters and digits, the
// words CleanTitle starts from.
func Tokens(title string) []string {
	return strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// IsQuantity reports whether CleanTitle drops w as a number or size.
func IsQuantity(w string) bool { return isQuantity(w) }

// IsUnit reports whether w can follow a number as its unit ("gb", "zoll").
func IsUnit(w string) bool { return units[w] || sizeUnits[w] }
```

Run: `go test ./internal/fingerprint/` → `ok` (unchanged tests).

- [ ] **Step 2: Write the failing tests** (`internal/product/product_test.go`)

```go
package product

import (
	"slices"
	"testing"
)

func TestOf(t *testing.T) {
	tests := []struct {
		title                string
		models, sizes, words []string
	}{
		{"Samsung Galaxy S24 128GB Onyx Black", []string{"s24"}, []string{"128gb"}, []string{"black", "galaxy", "onyx", "samsung"}},
		{"Galaxy S24 128 GB", []string{"s24"}, []string{"128gb"}, []string{"galaxy"}},
		{"LG OLED55C47LA 55 Zoll 4K", []string{"oled55c47la"}, []string{"55zoll"}, []string{"lg"}},
		{"Galaxy S24 5G für 549€", []string{"s24"}, nil, []string{"galaxy"}},
		{"Ryzen 7 7800X3D Prozessor", []string{"7800x3d"}, nil, []string{"prozessor", "ryzen"}},
		{"Anker Powerbank 20000mAh 2x USB-C", nil, []string{"20000mah", "2x"}, []string{"anker", "powerbank", "usb"}},
		{"Flüge nach Rom ab 19,99 Euro", nil, nil, []string{"euro", "flüge", "rom"}},
		{"Hue Lampe 3er Pack 12 Monate", nil, []string{"3er"}, []string{"hue", "lampe", "monate", "pack"}},
		{"Galaxy Buds3 + Galaxy S24 Bundle", []string{"buds3", "s24"}, nil, []string{"bundle", "galaxy"}},
	}
	for _, tt := range tests {
		got := Of(tt.title)
		if !slices.Equal(got.Models, tt.models) || !slices.Equal(got.Sizes, tt.sizes) || !slices.Equal(got.Words, tt.words) {
			t.Errorf("Of(%q) = %+v; want models %v sizes %v words %v", tt.title, got, tt.models, tt.sizes, tt.words)
		}
	}
}

func TestSame(t *testing.T) {
	phone := "Samsung Galaxy S24 128GB Onyx Black"
	tests := []struct {
		a, b string
		want bool
	}{
		{phone, "Galaxy S24 128 GB", true},
		{phone, "Samsung Galaxy S24 Smartphone", true}, // one names no size
		{phone, "Galaxy S24 256GB", false},             // both name sizes, differently
		{phone, "Galaxy S25 128GB", false},             // other model
		{phone, "Galaxy Buds3 + Galaxy S24 Bundle", false},
		{"Samsung Galaxy S24 Smartphone", "Hülle für Galaxy S24", false}, // accessory: 1 of 2 words shared
		{"S24", "Galaxy S24 128GB", true},                                 // no other words on one side
		{"Anker Powerbank 20000mAh", "Anker Powerbank 20000mAh", false},   // no model code
	}
	for _, tt := range tests {
		if got := Same(Of(tt.a), Of(tt.b)); got != tt.want {
			t.Errorf("Same(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
		if got := Same(Of(tt.b), Of(tt.a)); got != tt.want {
			t.Errorf("Same(%q, %q) = %v, want %v (symmetry)", tt.b, tt.a, got, tt.want)
		}
	}
}

func TestIndex(t *testing.T) {
	tests := []struct {
		title, price     string
		models, currency string
		cents            int64
		ok               bool
	}{
		{"Galaxy S24 128GB", "549€", "s24", "EUR", 54900, true},
		{"Galaxy Buds3 + Galaxy S24", "£199", "buds3 s24", "GBP", 19900, true},
		{"Powerbank 20000mAh", "19€", "", "", 0, false},
		{"Galaxy S24", "Gratis", "", "", 0, false},
	}
	for _, tt := range tests {
		models, currency, cents, ok := Index(tt.title, tt.price)
		if models != tt.models || currency != tt.currency || cents != tt.cents || ok != tt.ok {
			t.Errorf("Index(%q, %q) = %q, %q, %d, %v", tt.title, tt.price, models, currency, cents, ok)
		}
	}
}
```

(`history_test.go`)

```go
package product

import (
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func cand(id, title, price string) Candidate {
	cents, currency, _ := ParsePrice(price)
	return Candidate{ID: id, URL: "https://example.test/" + id, Title: title, Merchant: "Example Shop",
		Price: price, Currency: currency, Cents: cents, FirstSeen: t0}
}

func TestCompareAbove(t *testing.T) {
	own := cand("own", "Samsung Galaxy S24 128GB", "599€")
	b := cand("b", "Galaxy S24 128GB", "549€")
	b.Merchant, b.FirstSeen = "Example Air", t0.AddDate(0, -2, 0)
	c := cand("c", "Galaxy S24 128GB", "579€")
	h := Compare(own, []Candidate{
		own, c, b,
		cand("d", "Galaxy S24 256GB", "499€"), // other size
		cand("e", "Galaxy S24 128GB", "£499"), // other currency
		cand("f", "Hülle Galaxy S24", "9€"),   // accessory
	})
	if h == nil {
		t.Fatal("no history")
	}
	if h.Status != StatusAbove || h.DiffCents != 5000 || h.Count != 2 || h.Currency != "EUR" {
		t.Errorf("history = %+v", h)
	}
	if h.Lowest.ID != "b" || h.Lowest.Merchant != "Example Air" || h.Lowest.Price != "549€" || !h.Lowest.FirstSeen.Equal(b.FirstSeen) {
		t.Errorf("lowest = %+v", h.Lowest)
	}
	if len(h.Matches) != 2 || h.Matches[0].ID != "b" || h.Matches[1].ID != "c" {
		t.Errorf("matches = %+v", h.Matches)
	}
}

func TestCompareBestAndTie(t *testing.T) {
	other := cand("b", "Galaxy S24 128GB", "549€")
	for _, price := range []string{"529€", "549€"} {
		h := Compare(cand("own", "Galaxy S24 128GB", price), []Candidate{other})
		if h == nil || h.Status != StatusBest || h.DiffCents != 0 || h.Count != 1 {
			t.Errorf("price %s: history = %+v", price, h)
		}
	}
}

func TestCompareNone(t *testing.T) {
	other := cand("b", "Galaxy S24 128GB", "549€")
	for name, own := range map[string]Candidate{
		"no match":   cand("own", "Galaxy S25 128GB", "599€"),
		"no model":   cand("own", "Anker Powerbank", "19€"),
		"no price":   cand("own", "Galaxy S24 128GB", "Gratis"),
		"only itself": cand("b", "Galaxy S24 128GB", "549€"),
	} {
		if h := Compare(own, []Candidate{other}); h != nil {
			t.Errorf("%s: history = %+v, want nil", name, h)
		}
	}
}

func TestCompareCapsAndOrdersMatches(t *testing.T) {
	var cands []Candidate
	for i := range 12 {
		c := cand(fmt.Sprintf("c%02d", i), "Galaxy S24", fmt.Sprintf("%d€", 500+i))
		cands = append(cands, c)
	}
	older := cand("older", "Galaxy S24", "500€")
	older.FirstSeen = t0.AddDate(0, -1, 0)
	cands = append(cands, older)
	h := Compare(cand("own", "Galaxy S24", "600€"), cands)
	if h.Count != 13 || len(h.Matches) != MaxMatches {
		t.Fatalf("count %d, %d matches", h.Count, len(h.Matches))
	}
	if h.Matches[0].ID != "c00" || h.Matches[1].ID != "older" { // same price: newest first
		t.Errorf("first matches = %s, %s", h.Matches[0].ID, h.Matches[1].ID)
	}
}
```

- [ ] **Step 3: Run them and see them fail**

Run: `go test ./internal/product/`
Expected: FAIL (`undefined: Of`, `undefined: Compare`, …).

- [ ] **Step 4: Implement `product.go`**

```go
package product

import (
	"slices"
	"strings"
	"unicode"

	"github.com/MorrisMorrison/dealsieve/internal/fingerprint"
)

// Info is what Same compares: a title's model codes ("s24", "7800x3d"),
// sizes ("128gb", "55zoll") and remaining cleaned words, each sorted and
// distinct.
type Info struct {
	Models, Sizes, Words []string
}

// notSizes look like sizes but name a feature (network, resolution).
var notSizes = map[string]bool{"3g": true, "4g": true, "5g": true, "4k": true, "8k": true}

// notSizeUnits follow numbers that are prices or times, not product sizes.
var notSizeUnits = map[string]bool{
	"eur": true, "euro": true, "ct": true, "uhr": true, "h": true, "min": true,
	"s": true, "d": true, "tage": true, "jahre": true, "monate": true,
}

// Of splits a title into model codes, sizes and other words. A number
// followed by a unit word is one size ("16 GB" → "16gb").
func Of(title string) Info {
	tokens := fingerprint.Tokens(title)
	models, sizes, joined := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := 0; i < len(tokens); i++ {
		w := tokens[i]
		switch {
		case notSizes[w]:
		case isNumber(w):
			if i+1 < len(tokens) && fingerprint.IsUnit(tokens[i+1]) && !notSizeUnits[tokens[i+1]] {
				sizes[w+tokens[i+1]] = true
				joined[tokens[i+1]] = true
				i++
			}
		case fingerprint.IsQuantity(w):
			if unit := strings.TrimLeftFunc(w, unicode.IsDigit); unit != w && !notSizeUnits[unit] {
				sizes[w] = true
			}
		case strings.ContainsFunc(w, unicode.IsDigit) && strings.ContainsFunc(w, unicode.IsLetter):
			models[w] = true
		}
	}
	words := map[string]bool{}
	for _, w := range strings.Fields(fingerprint.CleanTitle(title)) {
		if !models[w] && !joined[w] {
			words[w] = true
		}
	}
	return Info{Models: sortedKeys(models), Sizes: sortedKeys(sizes), Words: sortedKeys(words)}
}

// Same reports whether two titles name the same product: identical model
// codes, the same sizes when both name any, and at least 60% of the smaller
// set of other words shared.
func Same(a, b Info) bool {
	if len(a.Models) == 0 || !slices.Equal(a.Models, b.Models) {
		return false
	}
	if len(a.Sizes) > 0 && len(b.Sizes) > 0 && !slices.Equal(a.Sizes, b.Sizes) {
		return false
	}
	if len(a.Words) == 0 || len(b.Words) == 0 {
		return true
	}
	shared := 0
	for _, w := range a.Words {
		if _, ok := slices.BinarySearch(b.Words, w); ok {
			shared++
		}
	}
	return shared*5 >= min(len(a.Words), len(b.Words))*3
}

// Index is what the store keeps per deal to find candidates: the model codes
// joined by spaces, and the parsed price. ok is false for deals without a
// model code or a price; they have no price history.
func Index(title, price string) (models, currency string, cents int64, ok bool) {
	info := Of(title)
	cents, currency, ok = ParsePrice(price)
	if !ok || len(info.Models) == 0 {
		return "", "", 0, false
	}
	return strings.Join(info.Models, " "), currency, cents, true
}

func isNumber(w string) bool {
	return w != "" && strings.TrimLeftFunc(w, unicode.IsDigit) == ""
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
```

- [ ] **Step 5: Implement `history.go`**

```go
package product

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

const (
	StatusBest  = "best"  // at or below the lowest price seen elsewhere
	StatusAbove = "above" // more than the lowest price seen
	MaxMatches  = 10
)

// Candidate is a stored deal with its parsed price.
type Candidate struct {
	ID, URL, Title, Merchant, Price, Currency string
	Cents                                     int64
	FirstSeen                                 time.Time
}

// Match is another deal of the same product, as the page shows it.
type Match struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Merchant  string    `json:"merchant"`
	Price     string    `json:"price"`
	FirstSeen time.Time `json:"first_seen"`
}

// History compares one deal's price with the other deals of its product.
type History struct {
	Status    string  `json:"status"`
	Currency  string  `json:"currency"`
	DiffCents int64   `json:"diff_cents"`
	Count     int     `json:"count"`
	Lowest    Match   `json:"lowest"`
	Matches   []Match `json:"matches"`
}

// Compare returns own's price history among candidates: the other deals of
// the same product in the same currency, cheapest first (same price: newest
// first), at most MaxMatches of them. It returns nil when own has no model
// code or price, or nothing matches.
func Compare(own Candidate, candidates []Candidate) *History {
	info := Of(own.Title)
	if own.Cents <= 0 || own.Currency == "" || len(info.Models) == 0 {
		return nil
	}
	var same []Candidate
	for _, c := range candidates {
		if c.ID == own.ID || c.Currency != own.Currency || c.Cents <= 0 || !Same(info, Of(c.Title)) {
			continue
		}
		same = append(same, c)
	}
	if len(same) == 0 {
		return nil
	}
	slices.SortFunc(same, func(a, b Candidate) int {
		if c := cmp.Compare(a.Cents, b.Cents); c != 0 {
			return c
		}
		if c := b.FirstSeen.Compare(a.FirstSeen); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	h := &History{Status: StatusBest, Currency: own.Currency, Count: len(same)}
	if own.Cents > same[0].Cents {
		h.Status, h.DiffCents = StatusAbove, own.Cents-same[0].Cents
	}
	for _, c := range same[:min(len(same), MaxMatches)] {
		h.Matches = append(h.Matches, Match{ID: c.ID, URL: c.URL, Title: c.Title, Merchant: c.Merchant, Price: c.Price, FirstSeen: c.FirstSeen})
	}
	h.Lowest = h.Matches[0]
	return h
}
```

- [ ] **Step 6: Run and see them pass**

Run: `go test ./internal/product/ ./internal/fingerprint/`
Expected: `ok` for both. If a `TestOf` row disagrees, check the token rules against the spec ("Same product" section) before changing code or the expectation, and report any change.

- [ ] **Step 7: Gates and commit**

```bash
gofmt -l . ; go vet ./... && go test ./...
git add internal/fingerprint/fingerprint.go internal/product/
git commit -m "feat(product): recognise the same product and compare prices

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Store — `deal_products`

**Files:**
- Create: `internal/store/migrations/004_price_history.sql`, `internal/store/products.go`
- Test: `internal/store/products_test.go`

**Interfaces:**
- Consumes: nothing from `product` (the store never imports it).
- Produces:
  - `type ProductRow struct { ID, URL, Title, Merchant, Price, Models, Currency string; Cents int64; FirstSeen time.Time }`
  - `func (s *Store) SetProduct(ctx context.Context, dealID, models, currency string, cents int64) error`
  - `func (s *Store) DeleteProduct(ctx context.Context, dealID string) error`
  - `func (s *Store) ReindexProducts(ctx context.Context, index func(title, price string) (models, currency string, cents int64, ok bool)) (int, error)` — returns the number of rows written
  - `func (s *Store) ProductCandidates(ctx context.Context, models []string) ([]ProductRow, error)` — ordered by models, cents, id

- [ ] **Step 1: Write the failing tests** (`internal/store/products_test.go`; `ctx`, `t0`, `openTest`, `deal`, `mustUpsert` exist in `store_test.go`)

```go
package store

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func candidateIDs(t *testing.T, s *Store, models ...string) []string {
	t.Helper()
	rows, err := s.ProductCandidates(ctx, models)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestProductCandidates(t *testing.T) {
	s := openTest(t)
	for _, id := range []string{"a", "b", "c"} {
		mustUpsert(t, s, deal(id, nil), source.FeedTrending, "fp", t0)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SetProduct(ctx, "mydealz:a", "s24", "EUR", 59900))
	must(s.SetProduct(ctx, "mydealz:b", "s24", "EUR", 54900))
	must(s.SetProduct(ctx, "mydealz:c", "s25", "EUR", 100))

	rows, err := s.ProductCandidates(ctx, []string{"s24"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != "mydealz:b" || rows[1].ID != "mydealz:a" {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if r.URL != "https://www.mydealz.de/deals/x-b" || r.Title != "Deal b" || r.Merchant != "Shop" || r.Price != "9€" ||
		r.Models != "s24" || r.Currency != "EUR" || r.Cents != 54900 || !r.FirstSeen.Equal(t0) {
		t.Errorf("row = %+v", r)
	}

	must(s.SetProduct(ctx, "mydealz:a", "s24", "EUR", 50000)) // update in place
	if got := candidateIDs(t, s, "s24"); !slices.Equal(got, []string{"mydealz:a", "mydealz:b"}) {
		t.Errorf("after update: %v", got)
	}
	must(s.DeleteProduct(ctx, "mydealz:a"))
	if got := candidateIDs(t, s, "s24", "s25"); !slices.Equal(got, []string{"mydealz:b", "mydealz:c"}) {
		t.Errorf("after delete: %v", got)
	}
	if got := candidateIDs(t, s); got != nil {
		t.Errorf("no models: %v", got)
	}
}

func TestReindexProducts(t *testing.T) {
	s := openTest(t)
	titled := func(id, title string) source.Deal {
		d := deal(id, nil)
		d.Title = title
		return d
	}
	mustUpsert(t, s, titled("a", "Galaxy S24"), source.FeedTrending, "fp", t0)
	mustUpsert(t, s, titled("b", "Galaxy S24 Ultra"), source.FeedTrending, "fp", t0)
	mustUpsert(t, s, titled("c", "Kabel"), source.FeedTrending, "fp", t0)
	// A stale row for a deal that no longer indexes must go.
	if err := s.SetProduct(ctx, "mydealz:c", "old", "EUR", 1); err != nil {
		t.Fatal(err)
	}
	index := func(title, price string) (string, string, int64, bool) {
		if !strings.Contains(title, "S24") || price != "9€" {
			return "", "", 0, false
		}
		return "s24", "EUR", 900, true
	}
	n, err := s.ReindexProducts(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("indexed %d, want 2", n)
	}
	if got := candidateIDs(t, s, "s24", "old"); !slices.Equal(got, []string{"mydealz:a", "mydealz:b"}) {
		t.Errorf("candidates = %v", got)
	}
}

func TestProductRowsArePruned(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("a", nil), source.FeedTrending, "fp", t0)
	if err := s.SetProduct(ctx, "mydealz:a", "s24", "EUR", 900); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(ctx, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := candidateIDs(t, s, "s24"); got != nil {
		t.Errorf("candidates after prune = %v", got)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/store/`
Expected: FAIL (`s.SetProduct undefined`, …).

- [ ] **Step 3: Add the migration** (`internal/store/migrations/004_price_history.sql`)

```sql
-- One row per deal with a model code and a parsed price, for price history.
-- models: the title's model codes, sorted, space-separated.
CREATE TABLE deal_products (
    deal_id  TEXT PRIMARY KEY REFERENCES deals (id) ON DELETE CASCADE,
    models   TEXT NOT NULL,
    currency TEXT NOT NULL,
    cents    INTEGER NOT NULL
);
CREATE INDEX deal_products_models ON deal_products (models, currency);
```

- [ ] **Step 4: Implement** (`internal/store/products.go`)

```go
package store

import (
	"context"
	"strings"
	"time"
)

// ProductRow is a deal with its price-history index, for comparing prices.
type ProductRow struct {
	ID, URL, Title, Merchant, Price string
	Models, Currency                string
	Cents                           int64
	FirstSeen                       time.Time
}

// SetProduct records a deal's model codes and parsed price.
func (s *Store) SetProduct(ctx context.Context, dealID, models, currency string, cents int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO deal_products (deal_id, models, currency, cents) VALUES (?, ?, ?, ?)
		ON CONFLICT (deal_id) DO UPDATE SET models = excluded.models, currency = excluded.currency, cents = excluded.cents`,
		dealID, models, currency, cents)
	return err
}

// DeleteProduct removes a deal's index, e.g. when its price stopped parsing.
func (s *Store) DeleteProduct(ctx context.Context, dealID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deal_products WHERE deal_id = ?`, dealID)
	return err
}

// ReindexProducts rebuilds every deal's index with index, so deals stored
// before price history (or before a parser change) get the current one. It
// returns how many deals have an index.
func (s *Store) ReindexProducts(ctx context.Context, index func(title, price string) (models, currency string, cents int64, ok bool)) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	type row struct {
		id, models, currency string
		cents                int64
	}
	var indexed []row
	rows, err := tx.QueryContext(ctx, `SELECT id, title, price FROM deals`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id, title, price string
		if err := rows.Scan(&id, &title, &price); err != nil {
			rows.Close()
			return 0, err
		}
		if models, currency, cents, ok := index(title, price); ok {
			indexed = append(indexed, row{id, models, currency, cents})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM deal_products`); err != nil {
		return 0, err
	}
	for _, r := range indexed {
		if _, err := tx.ExecContext(ctx, `INSERT INTO deal_products (deal_id, models, currency, cents) VALUES (?, ?, ?, ?)`,
			r.id, r.models, r.currency, r.cents); err != nil {
			return 0, err
		}
	}
	return len(indexed), tx.Commit()
}

// ProductCandidates returns the deals whose model codes are one of models.
func (s *Store) ProductCandidates(ctx context.Context, models []string) ([]ProductRow, error) {
	if len(models) == 0 {
		return nil, nil
	}
	args := make([]any, len(models))
	for i, m := range models {
		args[i] = m
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.url, d.title, d.merchant, d.price, p.models, p.currency, p.cents, d.first_seen
		FROM deal_products p JOIN deals d ON d.id = p.deal_id
		WHERE p.models IN (?`+strings.Repeat(", ?", len(models)-1)+`)
		ORDER BY p.models, p.cents, d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductRow
	for rows.Next() {
		var r ProductRow
		var first int64
		if err := rows.Scan(&r.ID, &r.URL, &r.Title, &r.Merchant, &r.Price, &r.Models, &r.Currency, &r.Cents, &first); err != nil {
			return nil, err
		}
		r.FirstSeen = fromUnix(first)
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run and see them pass**

Run: `go test ./internal/store/`
Expected: `ok` (also `TestOpenIsRepeatable` with the new migration).

- [ ] **Step 6: Gates and commit**

```bash
gofmt -l . ; go vet ./... && go test ./...
git add internal/store/migrations/004_price_history.sql internal/store/products.go internal/store/products_test.go
git commit -m "feat(store): keep a price-history index per deal

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Wire it — ingest, startup, `/api/deals`

**Files:**
- Modify: `internal/pipeline/pipeline.go` (in `Ingest`, after `UpsertDeal`)
- Modify: `cmd/dealsieve/main.go` (after the `Rekey` block)
- Create: `internal/api/prices.go`
- Modify: `internal/api/api.go` (`dealJSON`, `listDeals`)
- Test: `internal/pipeline/pipeline_test.go`, `internal/api/api_test.go`

**Interfaces:**
- Consumes: `product.Index`, `product.Compare`, `product.Candidate`, `product.History` (Task 2); `Store.SetProduct`, `DeleteProduct`, `ReindexProducts`, `ProductCandidates`, `ProductRow` (Task 3).
- Produces: `price_history` (omitted when nil) on each deal of `GET /api/deals`, shaped like `product.History`.

- [ ] **Step 1: Write the failing pipeline test** (append to `internal/pipeline/pipeline_test.go`)

```go
func TestIngestIndexesProducts(t *testing.T) {
	h := newHarness(t, settings.When{})
	d := deal("a", "Samsung Galaxy S24 128GB", intp(200))
	h.ingest(t, source.FeedTrending, d)
	rows, err := h.st.ProductCandidates(ctx, []string{"s24"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "mydealz:a" || rows[0].Cents != 900 || rows[0].Currency != "EUR" {
		t.Fatalf("rows = %+v", rows)
	}
	d.Price = "Gratis"
	h.ingest(t, source.FeedTrending, d)
	if rows, _ := h.st.ProductCandidates(ctx, []string{"s24"}); len(rows) != 0 {
		t.Errorf("rows after price became unparseable = %+v", rows)
	}
}
```

If `settings.When{}` is not a valid zero value for `newHarness` in this file, use whatever `When` the neighbouring tests pass for a rule that never fires; the test doesn't look at notifications.

- [ ] **Step 2: Write the failing API test** (append to `internal/api/api_test.go`)

```go
func TestListDealsPriceHistory(t *testing.T) {
	h := newHarness(t)
	now := h.api.Now()
	seed := func(id, title, price string, firstSeen time.Time) {
		d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://example.test/" + id,
			Title: title, Merchant: "Example Air", Price: price, Temperature: intp(200)}
		if _, err := h.st.UpsertDeal(ctx, d, source.FeedTrending, strings.ToLower(title), "air|"+id, firstSeen); err != nil {
			t.Fatal(err)
		}
		if err := h.st.SaveVerdict(ctx, store.Verdict{DealID: d.ID, Decision: "pass", Score: 200, DecidedAt: firstSeen}); err != nil {
			t.Fatal(err)
		}
	}
	seed("p1", "Galaxy S24 128GB", "599€", now.Add(-time.Hour))
	seed("p2", "Samsung Galaxy S24 128GB", "549€", now.AddDate(0, 0, -40)) // too old to be listed, still history
	if _, err := h.st.ReindexProducts(ctx, product.Index); err != nil {
		t.Fatal(err)
	}

	_, body := h.do(t, "GET", "/api/deals", "")
	var got struct {
		Deals []struct {
			ID           string           `json:"id"`
			PriceHistory *product.History `json:"price_history"`
		} `json:"deals"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	byID := map[string]*product.History{}
	for _, d := range got.Deals {
		byID[d.ID] = d.PriceHistory
	}
	p1, ok := byID["mydealz:p1"]
	if !ok || p1 == nil {
		t.Fatalf("p1 has no price history: %s", body)
	}
	if p1.Status != "above" || p1.DiffCents != 5000 || p1.Count != 1 || p1.Lowest.ID != "mydealz:p2" || p1.Lowest.Price != "549€" {
		t.Errorf("p1 history = %+v", p1)
	}
	if byID["mydealz:a"] != nil {
		t.Errorf("deal without model code has history: %+v", byID["mydealz:a"])
	}
	if strings.Count(string(body), `"price_history"`) != 1 {
		t.Errorf("price_history should be omitted when empty: %s", body)
	}
}
```

Add `"github.com/MorrisMorrison/dealsieve/internal/product"` to the test imports. If `ctx` isn't a package-level variable in `api_test.go`, use `context.Background()`.

- [ ] **Step 3: Run them and see them fail**

Run: `go test ./internal/pipeline/ ./internal/api/`
Expected: the pipeline test fails (no rows), the API test fails (no `price_history`).

- [ ] **Step 4: Pipeline** — in `Ingest`, directly after the `UpsertDeal` error check, add:

```go
		if err := p.indexProduct(ctx, d); err != nil {
			return fmt.Errorf("index deal %s: %w", d.ID, err)
		}
```

and add this method to `pipeline.go` (import `github.com/MorrisMorrison/dealsieve/internal/product`):

```go
// indexProduct keeps the deal's price-history index current; a deal whose
// title lost its model code or whose price stopped parsing loses its row.
func (p *Pipeline) indexProduct(ctx context.Context, d source.Deal) error {
	models, currency, cents, ok := product.Index(d.Title, d.Price)
	if !ok {
		return p.Store.DeleteProduct(ctx, d.ID)
	}
	return p.Store.SetProduct(ctx, d.ID, models, currency, cents)
}
```

- [ ] **Step 5: Startup** — in `cmd/dealsieve/main.go`, after the `Rekey` block, add (import `internal/product`):

```go
	// Rebuild the price-history index: fills it for deals stored before it
	// existed and applies parser changes to old deals.
	if n, err := st.ReindexProducts(ctx, product.Index); err != nil {
		return fmt.Errorf("index products: %w", err)
	} else {
		log.Info("indexed products for price history", "deals", n)
	}
```

- [ ] **Step 6: API** — create `internal/api/prices.go`:

```go
package api

import (
	"context"

	"github.com/MorrisMorrison/dealsieve/internal/product"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

// priceHistories compares each listed deal's price with the other stored
// deals of the same product, found by one query on their model codes.
func (s *Server) priceHistories(ctx context.Context, listed []store.Listed) (map[string]*product.History, error) {
	own := map[string]product.Candidate{}
	modelsOf := map[string]string{}
	seen := map[string]bool{}
	var keys []string
	for _, l := range listed {
		models, currency, cents, ok := product.Index(l.Title, l.Price)
		if !ok {
			continue
		}
		own[l.ID] = product.Candidate{ID: l.ID, URL: l.URL, Title: l.Title, Merchant: l.Merchant,
			Price: l.Price, Currency: currency, Cents: cents, FirstSeen: l.FirstSeen}
		modelsOf[l.ID] = models
		if !seen[models] {
			seen[models] = true
			keys = append(keys, models)
		}
	}
	rows, err := s.Store.ProductCandidates(ctx, keys)
	if err != nil {
		return nil, err
	}
	candidates := map[string][]product.Candidate{}
	for _, r := range rows {
		candidates[r.Models] = append(candidates[r.Models], product.Candidate{ID: r.ID, URL: r.URL, Title: r.Title,
			Merchant: r.Merchant, Price: r.Price, Currency: r.Currency, Cents: r.Cents, FirstSeen: r.FirstSeen})
	}
	out := map[string]*product.History{}
	for id, c := range own {
		if h := product.Compare(c, candidates[modelsOf[id]]); h != nil {
			out[id] = h
		}
	}
	return out, nil
}
```

In `api.go`, add to `dealJSON` after `Reasons`:

```go
	PriceHistory *product.History `json:"price_history,omitempty"`
```

and in `listDeals`, after the `Temperatures` lookup:

```go
	histories, err := s.priceHistories(r.Context(), listed)
	if err != nil {
		s.fail(w, err)
		return
	}
```

and set `PriceHistory: histories[l.ID],` in the `dealJSON` literal.

- [ ] **Step 7: Run and see them pass**

Run: `go test ./internal/pipeline/ ./internal/api/ ./cmd/...`
Expected: `ok`.

- [ ] **Step 8: Gates, e2e, commit**

```bash
gofmt -l . ; go vet ./... && go test ./... && go test -tags e2e ./test/e2e/
git add internal/pipeline/ internal/api/ cmd/dealsieve/main.go
git commit -m "feat: price history in /api/deals, indexed on ingest and at startup

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Page — chip and detail section

**Files:**
- Modify: `web/static/js/format.js`, `web/static/js/format.test.js`
- Create: `web/static/js/history.js`, `web/static/js/history.test.js`
- Modify: `web/static/js/render.js`, `web/index.html`, `web/static/css/styles.css`

**Interfaces:**
- Consumes: `deal.price_history` from `/api/deals` (Task 4): `{status, currency, diff_cents, count, lowest: {id,url,title,merchant,price,first_seen}, matches: [...]}` or absent.
- Produces: `money(cents, currency, locale)`, `monthYear(iso, locale)` in `format.js`; `historyChip(h, locale)` → `{text, cls}` or `null`, `historyLine(h, locale)` → string in `history.js`.

- [ ] **Step 1: Failing tests.** Append to `web/static/js/format.test.js` (add `money, monthYear` to its import from `./format.js`):

```js
test('money', () => {
  assert.equal(money(4000, 'EUR', 'de-DE'), '40 €');
  assert.equal(money(129999, 'EUR', 'de-DE'), '1.299,99 €');
  assert.equal(money(1250, 'GBP', 'en-GB'), '£12.50');
});

test('monthYear', () => {
  assert.equal(monthYear('2026-08-14T09:12:00Z', 'en-GB'), 'Aug 2026');
  assert.equal(monthYear('', 'en-GB'), '');
});
```

Create `web/static/js/history.test.js`:

```js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { historyChip, historyLine } from './history.js';

const lowest = { id: 'mydealz:2', url: 'https://example.test/2', title: 'Galaxy S24', merchant: 'Example Air', price: '549€', first_seen: '2026-08-14T09:12:00Z' };
const above = { status: 'above', currency: 'EUR', diff_cents: 4000, count: 3, lowest, matches: [lowest] };
const best = { status: 'best', currency: 'EUR', diff_cents: 0, count: 1, lowest, matches: [lowest] };

test('historyChip', () => {
  assert.equal(historyChip(undefined), null);
  assert.deepEqual(historyChip(best, 'de-DE'), { text: 'Best price', cls: 'best' });
  assert.deepEqual(historyChip(above, 'de-DE'), { text: '+40 €', cls: 'above' });
});

test('historyLine', () => {
  assert.equal(historyLine(undefined), '');
  assert.equal(historyLine(best, 'en-GB'), 'Best price in 180 days · seen once before');
  assert.equal(historyLine({ ...best, count: 4 }, 'en-GB'), 'Best price in 180 days · seen 4× before');
  assert.equal(historyLine(above, 'en-GB'), 'Lowest seen: 549€ at Example Air, Aug 2026 · €40 more than now');
  assert.equal(historyLine({ ...above, lowest: { ...lowest, merchant: '' } }, 'en-GB'), 'Lowest seen: 549€, Aug 2026 · €40 more than now');
});
```

Run: `node --test web/static/js/*.test.js` → FAIL (missing exports / module).

- [ ] **Step 2: Implement the helpers.** Append to `format.js`:

```js
// "40 €" / "£12.50": whole amounts without decimals.
export function money(cents, currency, locale) {
  const digits = cents % 100 === 0 ? 0 : 2;
  return new Intl.NumberFormat(locale, {
    style: 'currency', currency, minimumFractionDigits: digits, maximumFractionDigits: digits,
  }).format(cents / 100);
}

// "Aug 2026". UTC, so the month doesn't depend on the viewer's zone at a month's edge.
export function monthYear(iso, locale) {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return '';
  return new Date(t).toLocaleDateString(locale, { month: 'short', year: 'numeric', timeZone: 'UTC' });
}
```

Create `web/static/js/history.js`:

```js
import { money, monthYear } from './format.js';

// The list chip for a deal's price history, or null without one.
export function historyChip(h, locale) {
  if (!h) return null;
  if (h.status === 'best') return { text: 'Best price', cls: 'best' };
  return { text: `+${money(h.diff_cents, h.currency, locale)}`, cls: 'above' };
}

// The detail panel's summary line.
export function historyLine(h, locale) {
  if (!h) return '';
  if (h.status === 'best') {
    return `Best price in 180 days · ${h.count === 1 ? 'seen once before' : `seen ${h.count}× before`}`;
  }
  const where = h.lowest.merchant ? ` at ${h.lowest.merchant}` : '';
  return `Lowest seen: ${h.lowest.price}${where}, ${monthYear(h.lowest.first_seen, locale)} · ${money(h.diff_cents, h.currency, locale)} more than now`;
}
```

Run: `node --test web/static/js/*.test.js` → all pass.

- [ ] **Step 3: Markup.** In `web/index.html`, item template: after `<span class="chip"></span>` add `<span class="chip price-chip"></span>`. Detail template: after the closing `</figure>` add:

```html
      <section class="history" hidden>
        <h3 class="label">Price history</h3>
        <p class="history-line"></p>
        <details class="history-matches">
          <summary></summary>
          <ul></ul>
          <p class="meta small history-more" hidden>Showing the 10 cheapest.</p>
        </details>
      </section>
```

- [ ] **Step 4: Render.** In `render.js` add `import { historyChip, historyLine } from './history.js';` and add `monthYear` to the `./format.js` import. In `renderList`, after the block/watchlist chip code:

```js
    const history = historyChip(deal.price_history);
    if (history) {
      const priceChip = item.querySelector('.price-chip');
      priceChip.textContent = history.text;
      priceChip.classList.add(history.cls);
    }
```

In `renderDetail`, after the sparkline / first-seen code and before the "why" section:

```js
  renderHistory(node.querySelector('.history'), deal.price_history);
```

and add the function (near `renderStatus`):

```js
// Price history: a summary and the matched deals, so every match can be checked.
function renderHistory(section, history) {
  if (!history) return;
  section.hidden = false;
  section.querySelector('.history-line').textContent = historyLine(history);
  section.querySelector('summary').textContent = `Matched deals (${history.count})`;
  section.querySelector('ul').replaceChildren(...history.matches.map((m) => {
    const link = el('a', '', m.title);
    link.href = m.url;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    const li = el('li');
    li.append(el('span', 'price num', m.price), ' ',
      el('span', 'meta', [m.merchant, monthYear(m.first_seen)].filter(Boolean).join(' · ')), el('br'), link);
    return li;
  }));
  section.querySelector('.history-more').hidden = history.count <= history.matches.length;
}
```

- [ ] **Step 5: Styles.** In `styles.css`, after `.chip.block { … }`:

```css
.chip.best { background: color-mix(in srgb, var(--ok) 15%, transparent); color: var(--ok); }
.chip.above { background: var(--surface-2); color: var(--muted); }
.history-line { margin: 4px 0 8px; }
.history-matches summary { cursor: pointer; color: var(--muted); font-size: 13px; }
.history-matches ul { list-style: none; margin: 8px 0 0; padding: 0; display: grid; gap: 8px; font-size: 13px; }
.history-matches a { color: inherit; }
```

- [ ] **Step 6: Check on a local build.** Build the binary, run it against a fake feed with two deals of one product (e.g. temporarily point `DEALSIEVE_MYDEALZ_URL` at a small static server serving made-up RSS with "Galaxy S24 128GB" at 599€ and 549€ from different `Example` shops) and screenshot the list chip and the detail panel, light and dark, at desktop and 375 px. Delete any temporary files; commit nothing from this step.

- [ ] **Step 7: Gates and commit**

```bash
node --test web/static/js/*.test.js && gofmt -l . ; go vet ./... && go test ./... && go test -tags e2e ./test/e2e/
git add web/
git commit -m "feat(web): price history chip and detail section

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Docs

**Files:**
- Modify: `README.md` (after the "What it filters" list's inbox paragraph), `CLAUDE.md` (layout table, API line, a convention bullet), `docs-site/src/content/docs/guide/how-it-works.md` (new `## Price history` before `## The page`; a line in "What dealsieve does not do"), `docs-site/src/content/docs/reference/api.md` (`GET /api/deals` section)

- [ ] **Step 1: README** — after the paragraph starting "The page is an inbox", add:

```markdown
**Price history:** for products with a model code (`S24`, `7800X3D`, `WH-1000XM5`), the deal's
panel shows the lowest price dealsieve has seen for the same product in the last 180 days, at any
shop, with the matched deals listed so you can check them. The list marks best prices and how much
more a deal costs than the lowest. It's information only and never blocks a deal.
```

- [ ] **Step 2: CLAUDE.md** — add a layout row after `internal/fingerprint/`:

```markdown
| `internal/product/` | Pure: `ParsePrice` (cents + currency from `1.449€`, `£12.99`), `Of`/`Same` (same product = identical model codes, equal sizes when both name any, ≥ 60% shared other words), `Compare` (price history for one deal) |
```

In the `internal/store/` row, add "price-history index (`deal_products`)" to the list. Add a convention bullet after "Fingerprints":

```markdown
- **Price history is information only.** It never changes a verdict, score, notification or the digest. `deal_products` is rebuilt from all deals at startup (`ReindexProducts`), so a parser or matching change needs no migration.
```

- [ ] **Step 3: docs-site how-it-works** — before `## The page` add:

```markdown
## Price history

For a deal whose title has a model code (`S24`, `7800X3D`), dealsieve looks for other stored deals of the same product, at any shop, in the last 180 days: the same model codes, the same size when both titles name one (`128GB` vs `256GB` never match), and mostly the same other words (so a case "for Galaxy S24" doesn't count as the phone). Prices are compared only within one currency.

The deal's panel then shows either **Best price in 180 days** or the lowest price seen ("Lowest seen: 549€ at Example Shop, Aug 2026 · 40 € more than now"), with the matched deals listed so you can check them. The list shows a **Best price** or **+40 €** chip. Price history is information only: it never blocks or ranks a deal, and deals without a model code or a price have none.
```

In "What dealsieve does not do" add:

```markdown
- It does not know prices outside its own feeds: price history only covers deals dealsieve stored itself, and only products with a model code.
```

- [ ] **Step 4: docs-site api.md** — in `## GET /api/deals`, after the `counts` paragraph add:

```markdown
A deal can also carry `price_history` (left out when there is none): `status` (`best` or `above`), `currency`, `diff_cents` (how much more than the lowest; 0 for `best`), `count` (other deals of the same product), `lowest` and `matches` (up to 10, cheapest first; each with `id`, `url`, `title`, `merchant`, `price`, `first_seen`).
```

- [ ] **Step 5: Build the docs site and commit**

```bash
(cd docs-site && npm ci && npm run build)
git add README.md CLAUDE.md docs-site/src/content/docs/
git commit -m "docs: price history

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
