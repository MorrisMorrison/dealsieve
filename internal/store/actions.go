package store

import (
	"context"
	"strings"
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

// Click is a Hide or Junk action with a label a person can recognise.
type Click struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	Label     string    `json:"label"`
	URL       string    `json:"url,omitempty"`
}

// Clicks returns the newest Hide and Junk clicks (at most limit), each labelled
// with its deal's title and link. The deals come from two queries, not one per
// click. A Hide whose deal was pruned says so; a Junk with no deal left shows
// its fingerprint's words.
func (s *Store) Clicks(ctx context.Context, limit int) ([]Click, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, value, created_at FROM actions
		WHERE kind IN (?, ?) ORDER BY created_at DESC, id DESC LIMIT ?`, ActionHide, ActionJunk, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var clicks []Click
	var values []string
	var hideIDs, fingerprints []string
	for rows.Next() {
		var c Click
		var value string
		var created int64
		if err := rows.Scan(&c.ID, &c.Kind, &value, &created); err != nil {
			return nil, err
		}
		c.CreatedAt = fromUnix(created)
		clicks = append(clicks, c)
		values = append(values, value)
		if c.Kind == ActionHide {
			hideIDs = append(hideIDs, value)
		} else {
			fingerprints = append(fingerprints, value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID, err := s.dealsBy(ctx, "id", hideIDs)
	if err != nil {
		return nil, err
	}
	byFingerprint, err := s.dealsBy(ctx, "fingerprint", fingerprints)
	if err != nil {
		return nil, err
	}
	for i := range clicks {
		c := &clicks[i]
		if c.Kind == ActionHide {
			if d, ok := byID[values[i]]; ok {
				c.Label, c.URL = d.Title, d.URL
			} else {
				c.Label = "A deal that is no longer stored"
			}
			continue
		}
		if d, ok := byFingerprint[values[i]]; ok {
			c.Label, c.URL = d.Title, d.URL
		} else {
			_, words, _ := strings.Cut(values[i], "|")
			c.Label = words
		}
	}
	return clicks, nil
}

// dealsBy maps each value of column (id or fingerprint) to its most recently
// first-seen deal.
func (s *Store) dealsBy(ctx context.Context, column string, values []string) (map[string]struct{ Title, URL string }, error) {
	out := map[string]struct{ Title, URL string }{}
	if len(values) == 0 {
		return out, nil
	}
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+column+`, title, url FROM deals WHERE `+column+` IN (?`+strings.Repeat(", ?", len(values)-1)+`)
		ORDER BY first_seen DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, title, url string
		if err := rows.Scan(&key, &title, &url); err != nil {
			return nil, err
		}
		if _, seen := out[key]; !seen {
			out[key] = struct{ Title, URL string }{title, url}
		}
	}
	return out, rows.Err()
}
