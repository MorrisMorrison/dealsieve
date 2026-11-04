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
