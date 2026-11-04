package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MaxDigestHits caps the watchlist recap in one digest.
const MaxDigestHits = 10

// DigestCutoff returns when the channel's last successful digest window
// ended; false if it never had one.
func (s *Store) DigestCutoff(ctx context.Context, channel string) (time.Time, bool, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT last_cutoff FROM digest_state WHERE channel = ?`, channel).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return fromUnix(at), true, nil
}

// SetDigestCutoff moves the channel's window end to at. Call it only after
// a successful send or for an empty window; otherwise use CompleteDigest.
func (s *Store) SetDigestCutoff(ctx context.Context, channel string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO digest_state (channel, last_cutoff) VALUES (?, ?)
		ON CONFLICT (channel) DO UPDATE SET last_cutoff = excluded.last_cutoff`, channel, at.Unix())
	return err
}

// CompleteDigest records, after a successful send, that the channel got
// dealIDs and that its window now ends at cutoff.
func (s *Store) CompleteDigest(ctx context.Context, channel string, dealIDs []string, cutoff time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range dealIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO digest_sent (channel, deal_id, sent_at) VALUES (?, ?, ?)`,
			channel, id, cutoff.Unix()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO digest_state (channel, last_cutoff) VALUES (?, ?)
		ON CONFLICT (channel) DO UPDATE SET last_cutoff = excluded.last_cutoff`, channel, cutoff.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// DigestDeals returns the deals for the channel's next digest: passing now,
// seen in a feed after since, and not in an earlier digest of the channel.
// Deals usually pass hours after they are first seen, so the window is on
// last_seen, and digest_sent keeps a deal from being listed twice. Watchlist
// hits come newest first (at most MaxDigestHits), the others by temperature,
// unknown last, then newest first (at most top). sources limits the deals to
// those sources; nil means every source, an empty slice none.
func (s *Store) DigestDeals(ctx context.Context, channel string, sources []string, since time.Time, top int) (hits, others []DealRecord, err error) {
	filter, fargs := sourceFilter(sources)
	base := `SELECT ` + dealColumns + ` FROM deals d JOIN verdicts v ON v.deal_id = d.id
		WHERE v.decision = 'pass' AND v.watchlist_hit = ? AND d.last_seen > ?
			AND NOT EXISTS (SELECT 1 FROM digest_sent s WHERE s.channel = ? AND s.deal_id = d.id)` + filter + ` `
	hits, err = s.digestQuery(ctx, base+`ORDER BY d.first_seen DESC, d.id LIMIT ?`, 1, since, channel, fargs, MaxDigestHits)
	if err != nil {
		return nil, nil, err
	}
	others, err = s.digestQuery(ctx, base+`ORDER BY d.temperature IS NULL, d.temperature DESC, d.first_seen DESC, d.id LIMIT ?`, 0, since, channel, fargs, top)
	return hits, others, err
}

func (s *Store) digestQuery(ctx context.Context, query string, watch int, since time.Time, channel string, fargs []any, limit int) ([]DealRecord, error) {
	args := append(append([]any{watch, since.Unix(), channel}, fargs...), limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DealRecord
	for rows.Next() {
		rec, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
