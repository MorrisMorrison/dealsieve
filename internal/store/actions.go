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

func (s *Store) AddAction(ctx context.Context, kind, value string, now time.Time) (Action, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO actions (kind, value, created_at) VALUES (?, ?, ?)`, kind, value, now.Unix())
	if err != nil {
		return Action{}, err
	}
	id, err := res.LastInsertId()
	return Action{ID: id, Kind: kind, Value: value, CreatedAt: fromUnix(now.Unix())}, err
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
