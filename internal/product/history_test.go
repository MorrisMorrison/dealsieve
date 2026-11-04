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
		"no match":    cand("own", "Galaxy S25 128GB", "599€"),
		"no model":    cand("own", "Anker Powerbank", "19€"),
		"no price":    cand("own", "Galaxy S24 128GB", "Gratis"),
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
