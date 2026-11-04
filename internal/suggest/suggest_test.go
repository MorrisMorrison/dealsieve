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
