package store

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func TestCountRecurrenceIsPerSource(t *testing.T) {
	st := openTest(t)
	t1 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	add := func(id, src string, at time.Time) DealRecord {
		rec, err := st.UpsertDeal(ctx, source.Deal{ID: id, Source: src, URL: "https://example.test/" + id, Title: "Example Echo"},
			source.FeedTrending, "example echo", "example shop|echo", at)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	add("mydealz:1", "mydealz", t1)
	add("preisjaeger:1", "preisjaeger", t1.Add(time.Hour))
	last := add("mydealz:2", "mydealz", t1.Add(2*time.Hour))
	n, _, err := st.CountRecurrence(ctx, "mydealz", last.Fingerprint, last.ID, last.FirstSeen, 90*24*time.Hour)
	if err != nil || n != 1 {
		t.Errorf("mydealz recurrence = %d, %v; want 1 (the preisjaeger deal doesn't count)", n, err)
	}
}

func TestListingsFilterBySource(t *testing.T) {
	st := openTest(t)
	add := func(id, src, category string) {
		d := source.Deal{ID: id, Source: src, URL: "https://example.test/" + id, Title: "Example " + id, Category: category, Temperature: intp(200)}
		mustUpsert(t, st, d, source.FeedTrending, "fp-"+id, t0)
		save(t, st, id, "pass", 200, false)
	}
	add("mydealz:1", "mydealz", "Gaming")
	add("hotukdeals:1", "hotukdeals", "Elektronik")
	since := t0.Add(-time.Hour)

	for _, tc := range []struct {
		name    string
		sources []string
		want    []string
		cats    []string
	}{
		{"nil sees every source", nil, []string{"hotukdeals:1", "mydealz:1"}, []string{"Elektronik", "Gaming"}},
		{"one source", []string{"hotukdeals"}, []string{"hotukdeals:1"}, []string{"Elektronik"}},
		{"two sources", []string{"mydealz", "hotukdeals"}, []string{"hotukdeals:1", "mydealz:1"}, []string{"Elektronik", "Gaming"}},
		{"empty slice sees nothing", []string{}, nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := st.ListDeals(ctx, ViewPassed, tc.sources, since, 100)
			if err != nil {
				t.Fatal(err)
			}
			gotIDs := ids(got)
			slices.Sort(gotIDs)
			if !slices.Equal(gotIDs, tc.want) {
				t.Errorf("ListDeals = %v, want %v", gotIDs, tc.want)
			}
			counts, err := st.CountViews(ctx, tc.sources, since)
			if err != nil {
				t.Fatal(err)
			}
			if want := map[string]int{ViewPassed: len(tc.want), ViewWatchlist: 0, ViewBlocked: 0}; !maps.Equal(counts, want) {
				t.Errorf("CountViews = %v, want %v", counts, want)
			}
			cats, err := st.Categories(ctx, tc.sources)
			if err != nil || !slices.Equal(cats, tc.cats) {
				t.Errorf("Categories = %q, %v; want %q", cats, err, tc.cats)
			}
		})
	}

	hits, others, err := st.DigestDeals(ctx, "phone", []string{"mydealz"}, since, 10)
	if err != nil || len(hits) != 0 || !slices.Equal(dealIDs(others), []string{"mydealz:1"}) {
		t.Errorf("DigestDeals(mydealz) = %v, %v, %v", dealIDs(hits), dealIDs(others), err)
	}
	_, others, err = st.DigestDeals(ctx, "phone", nil, since, 10)
	if err != nil || len(others) != 2 {
		t.Errorf("DigestDeals(nil) = %v, %v; want both", dealIDs(others), err)
	}
	_, others, err = st.DigestDeals(ctx, "phone", []string{}, since, 10)
	if err != nil || len(others) != 0 {
		t.Errorf("DigestDeals(empty) = %v, %v; want none", dealIDs(others), err)
	}
}
