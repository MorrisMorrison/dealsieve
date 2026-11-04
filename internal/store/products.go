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
