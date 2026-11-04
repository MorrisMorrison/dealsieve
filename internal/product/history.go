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

// Candidate is a stored deal with its parsed price. Info may carry Of(Title)
// when the caller has it already; Compare parses the title otherwise.
type Candidate struct {
	ID, URL, Title, Merchant, Price, Currency string
	Cents                                     int64
	FirstSeen                                 time.Time
	Info                                      Info
}

func (c Candidate) info() Info {
	if len(c.Info.Models) > 0 {
		return c.Info
	}
	return Of(c.Title)
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
	info := own.info()
	if own.Cents <= 0 || own.Currency == "" || len(info.Models) == 0 {
		return nil
	}
	var same []Candidate
	for _, c := range candidates {
		if c.ID == own.ID || c.Currency != own.Currency || c.Cents <= 0 || !Same(info, c.info()) {
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
