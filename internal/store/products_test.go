package store

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func productIDs(t *testing.T, s *Store, models ...string) []string {
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
	if got := productIDs(t, s, "s24"); !slices.Equal(got, []string{"mydealz:a", "mydealz:b"}) {
		t.Errorf("after update: %v", got)
	}
	must(s.DeleteProduct(ctx, "mydealz:a"))
	if got := productIDs(t, s, "s24", "s25"); !slices.Equal(got, []string{"mydealz:b", "mydealz:c"}) {
		t.Errorf("after delete: %v", got)
	}
	if got := productIDs(t, s); got != nil {
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
	if got := productIDs(t, s, "s24", "old"); !slices.Equal(got, []string{"mydealz:a", "mydealz:b"}) {
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
	if got := productIDs(t, s, "s24"); got != nil {
		t.Errorf("candidates after prune = %v", got)
	}
}
