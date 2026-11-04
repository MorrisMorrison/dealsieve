// Package rules decides whether a deal passes, and records why.
package rules

import (
	"fmt"
	"strings"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
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
	Reasons      []string // the block reason, if any, comes first; a pass ends with its pass reasons
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

func New(r settings.Rules, watchlist []string) *Engine {
	e := &Engine{
		minTemperature:  r.MinTemperature,
		overrides:       make(map[string]int, len(r.CategoryOverrides)),
		mutedCategories: lowerSet(r.MutedCategories),
		mutedMerchants:  lowerSet(r.MutedMerchants),
		blockKeywords:   lowerAll(r.BlockKeywords),
		watchlist:       lowerAll(watchlist),
		window:          r.Recurrence.Window(),
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
	} else {
		res.Reasons = append(res.Reasons, e.passReasons(d, f, strings.ToLower(d.Category), res.WatchlistHit)...)
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
	// A watchlist hit is explicit intent; fingerprints drop model numbers, so
	// different products from one shop can share a key and look recurring.
	if f.Recurrence >= e.maxSeen && !watchlistHit {
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

// passReasons says why a deal passed: its temperature against the minimum and
// how often it was seen before. A watchlist hit may pass despite either.
func (e *Engine) passReasons(d Deal, f Facts, category string, watchlistHit bool) []string {
	var out []string
	if d.Temperature != nil {
		if minimum := e.minTemperatureFor(category); *d.Temperature >= minimum {
			out = append(out, fmt.Sprintf("hot: %d° (minimum %d°)", *d.Temperature, minimum))
		} else {
			out = append(out, fmt.Sprintf("below the %d° minimum, kept for your watchlist", minimum))
		}
	}
	days := int(e.window.Hours() / 24)
	switch {
	case f.Recurrence == 0:
		out = append(out, fmt.Sprintf("new offer: not seen in the last %d days", days))
	case f.Recurrence < e.maxSeen:
		out = append(out, fmt.Sprintf("seen %d× before in the last %d days (limit %d)", f.Recurrence, days, e.maxSeen))
	default:
		out = append(out, fmt.Sprintf("seen %d× before in the last %d days, kept for your watchlist", f.Recurrence, days))
	}
	return out
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
