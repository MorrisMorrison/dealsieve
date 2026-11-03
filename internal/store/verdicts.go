package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Verdict struct {
	DealID       string
	Decision     string
	Score        int
	WatchlistHit bool
	Reasons      []string
	DecidedAt    time.Time
}

type Listed struct {
	DealRecord
	Verdict Verdict
}

const (
	ViewPassed    = "passed"
	ViewWatchlist = "watchlist"
	ViewBlocked   = "blocked"
)

// A deal only in the new feed is a page candidate only as a watchlist hit.
var viewFilters = map[string]string{
	ViewPassed:    `v.decision = 'pass' AND d.in_trending = 1`,
	ViewWatchlist: `v.decision = 'pass' AND v.watchlist_hit = 1`,
	ViewBlocked:   `v.decision = 'block' AND (d.in_trending = 1 OR v.watchlist_hit = 1)`,
}

func ValidView(v string) bool {
	_, ok := viewFilters[v]
	return ok
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func saveVerdict(ctx context.Context, x execer, v Verdict) error {
	reasons := v.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	raw, err := json.Marshal(reasons)
	if err != nil {
		return err
	}
	_, err = x.ExecContext(ctx, `INSERT INTO verdicts (deal_id, decision, score, watchlist_hit, reasons, decided_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (deal_id) DO UPDATE SET decision = excluded.decision, score = excluded.score,
			watchlist_hit = excluded.watchlist_hit, reasons = excluded.reasons, decided_at = excluded.decided_at`,
		v.DealID, v.Decision, v.Score, v.WatchlistHit, string(raw), v.DecidedAt.Unix())
	return err
}

func (s *Store) SaveVerdict(ctx context.Context, v Verdict) error {
	return saveVerdict(ctx, s.db, v)
}

// SaveVerdicts writes all verdicts in one transaction.
func (s *Store) SaveVerdicts(ctx context.Context, vs []Verdict) error {
	if len(vs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range vs {
		if err := saveVerdict(ctx, tx, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetVerdict(ctx context.Context, dealID string) (Verdict, error) {
	v := Verdict{DealID: dealID}
	var reasons string
	var decided int64
	err := s.db.QueryRowContext(ctx, `SELECT decision, score, watchlist_hit, reasons, decided_at FROM verdicts WHERE deal_id = ?`, dealID).
		Scan(&v.Decision, &v.Score, &v.WatchlistHit, &reasons, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return Verdict{}, ErrNotFound
	}
	if err != nil {
		return Verdict{}, err
	}
	v.DecidedAt = fromUnix(decided)
	return v, json.Unmarshal([]byte(reasons), &v.Reasons)
}

// ListDeals returns a view's deals last seen since since, best score first.
func (s *Store) ListDeals(ctx context.Context, view string, since time.Time, limit int) ([]Listed, error) {
	where, ok := viewFilters[view]
	if !ok {
		return nil, fmt.Errorf("unknown view %q", view)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+dealColumns+`, v.decision, v.score, v.watchlist_hit, v.reasons, v.decided_at
		FROM deals d JOIN verdicts v ON v.deal_id = d.id
		WHERE `+where+` AND d.last_seen >= ?
		ORDER BY v.score DESC, d.first_seen DESC, d.id LIMIT ?`, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Listed
	for rows.Next() {
		var l Listed
		var reasons string
		var decided int64
		rec, err := scanDeal(rows, &l.Verdict.Decision, &l.Verdict.Score, &l.Verdict.WatchlistHit, &reasons, &decided)
		if err != nil {
			return nil, err
		}
		l.DealRecord = rec
		l.Verdict.DealID = rec.ID
		l.Verdict.DecidedAt = fromUnix(decided)
		if err := json.Unmarshal([]byte(reasons), &l.Verdict.Reasons); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
