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
			Price: l.Price, Currency: currency, Cents: cents, FirstSeen: l.FirstSeen, Info: product.Of(l.Title)}
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
			Merchant: r.Merchant, Price: r.Price, Currency: r.Currency, Cents: r.Cents, FirstSeen: r.FirstSeen,
			Info: product.Of(r.Title)})
	}
	out := map[string]*product.History{}
	for id, c := range own {
		if h := product.Compare(c, candidates[modelsOf[id]]); h != nil {
			out[id] = h
		}
	}
	return out, nil
}
