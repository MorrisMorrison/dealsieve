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
