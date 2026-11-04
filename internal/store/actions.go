package store

import (
	"context"
	"time"
)

const (
	ActionHide         = "hide_deal"     // value: deal ID
	ActionJunk         = "junk"          // value: fingerprint
	ActionMuteCategory = "mute_category" // value: category
	ActionMuteMerchant = "mute_merchant" // value: merchant
)

func ValidActionKind(k string) bool {
	switch k {
	case ActionHide, ActionJunk, ActionMuteCategory, ActionMuteMerchant:
		return true
	}
	return false
}

type Action struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
}

// AddAction stores a click. The same (kind, value) is stored once: a repeat
// returns the existing action unchanged.
func (s *Store) AddAction(ctx context.Context, kind, value string, now time.Time) (Action, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO actions (kind, value, created_at) VALUES (?, ?, ?)
		ON CONFLICT (kind, value) DO NOTHING`, kind, value, now.Unix()); err != nil {
		return Action{}, err
	}
	a := Action{Kind: kind, Value: value}
	var created int64
	if err := s.db.QueryRowContext(ctx, `SELECT id, created_at FROM actions WHERE kind = ? AND value = ?`, kind, value).Scan(&a.ID, &created); err != nil {
		return Action{}, err
	}
	a.CreatedAt = fromUnix(created)
	return a, nil
}

func (s *Store) DeleteAction(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM actions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Actions returns all actions, newest first.
func (s *Store) Actions(ctx context.Context) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, value, created_at FROM actions ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var a Action
		var created int64
		if err := rows.Scan(&a.ID, &a.Kind, &a.Value, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = fromUnix(created)
		out = append(out, a)
	}
	return out, rows.Err()
}
