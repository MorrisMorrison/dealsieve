package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type SettingsVersion struct {
	ID        int64
	Document  []byte
	Summary   []string
	CreatedAt time.Time
}

const settingsColumns = `id, document, summary, created_at`

func scanSettings(sc scanner) (SettingsVersion, error) {
	var v SettingsVersion
	var doc, summary string
	var created int64
	if err := sc.Scan(&v.ID, &doc, &summary, &created); err != nil {
		return SettingsVersion{}, err
	}
	v.Document, v.CreatedAt = []byte(doc), fromUnix(created)
	return v, json.Unmarshal([]byte(summary), &v.Summary)
}

func (s *Store) SaveSettings(ctx context.Context, document []byte, summary []string, now time.Time) (SettingsVersion, error) {
	if summary == nil {
		summary = []string{}
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return SettingsVersion{}, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO settings_versions (document, summary, created_at) VALUES (?, ?, ?)`,
		string(document), string(raw), now.Unix())
	if err != nil {
		return SettingsVersion{}, err
	}
	id, err := res.LastInsertId()
	return SettingsVersion{ID: id, Document: document, Summary: summary, CreatedAt: fromUnix(now.Unix())}, err
}

func (s *Store) LatestSettings(ctx context.Context) (SettingsVersion, error) {
	return s.oneSettings(ctx, `SELECT `+settingsColumns+` FROM settings_versions ORDER BY id DESC LIMIT 1`)
}

// SettingsBefore returns the newest version older than id.
func (s *Store) SettingsBefore(ctx context.Context, id int64) (SettingsVersion, error) {
	return s.oneSettings(ctx, `SELECT `+settingsColumns+` FROM settings_versions WHERE id < ? ORDER BY id DESC LIMIT 1`, id)
}

func (s *Store) oneSettings(ctx context.Context, query string, args ...any) (SettingsVersion, error) {
	v, err := scanSettings(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return SettingsVersion{}, ErrNotFound
	}
	return v, err
}

// SettingsHistory returns up to limit versions, newest first.
func (s *Store) SettingsHistory(ctx context.Context, limit int) ([]SettingsVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+settingsColumns+` FROM settings_versions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SettingsVersion
	for rows.Next() {
		v, err := scanSettings(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DismissSuggestion(ctx context.Context, kind, value string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO dismissed_suggestions (kind, value, created_at) VALUES (?, ?, ?)`,
		kind, strings.ToLower(strings.TrimSpace(value)), now.Unix())
	return err
}

// DismissedSuggestions returns the dismissals keyed "kind|lowercase value".
func (s *Store) DismissedSuggestions(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, value FROM dismissed_suggestions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return nil, err
		}
		out[kind+"|"+value] = true
	}
	return out, rows.Err()
}

type ClickedDeal struct {
	DealID    string
	Title     string
	Merchant  string
	Category  string
	ClickedAt time.Time
}

// ClickedDealsSince returns the deals the user hid or marked as junk since
// since, oldest click first. A junk action stores a fingerprint, so it is
// represented by the most recently seen deal with that fingerprint. Clicks
// whose deal was pruned are left out.
func (s *Store) ClickedDealsSince(ctx context.Context, since time.Time) ([]ClickedDeal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.created_at AS clicked_at, a.id AS action_id, d.id, d.title, d.merchant, d.category
		FROM actions a JOIN deals d ON d.id = a.value
		WHERE a.kind = ? AND a.created_at >= ?
		UNION ALL
		SELECT a.created_at, a.id, d.id, d.title, d.merchant, d.category
		FROM actions a JOIN deals d ON d.id = (
			SELECT id FROM deals WHERE fingerprint = a.value ORDER BY last_seen DESC, id DESC LIMIT 1)
		WHERE a.kind = ? AND a.created_at >= ?
		ORDER BY clicked_at, action_id`,
		ActionHide, since.Unix(), ActionJunk, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClickedDeal
	for rows.Next() {
		var c ClickedDeal
		var at, actionID int64
		if err := rows.Scan(&at, &actionID, &c.DealID, &c.Title, &c.Merchant, &c.Category); err != nil {
			return nil, err
		}
		c.ClickedAt = fromUnix(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Categories lists the distinct categories of deals from sources; nil means
// every source, an empty slice none.
func (s *Store) Categories(ctx context.Context, sources []string) ([]string, error) {
	filter, args := sourceFilter(sources)
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT d.category FROM deals d WHERE d.category <> ''`+filter+` ORDER BY d.category`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteActionsByValue(ctx context.Context, kind, value string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM actions WHERE kind = ? AND value = ?`, kind, value)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
