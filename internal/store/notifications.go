package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PendingNotification struct {
	DealID  string
	Channel string
}

func (s *Store) NotificationState(ctx context.Context, dealID, channel string) (bool, int, error) {
	var sentAt sql.NullInt64
	var attempts int
	err := s.db.QueryRowContext(ctx, `SELECT sent_at, attempts FROM notifications_sent WHERE deal_id = ? AND channel = ?`, dealID, channel).
		Scan(&sentAt, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	return sentAt.Valid, attempts, err
}

// RecordNotification counts one send attempt; ok marks the deal as sent.
func (s *Store) RecordNotification(ctx context.Context, dealID, channel string, ok bool, now time.Time) error {
	var sentAt any
	if ok {
		sentAt = now.Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO notifications_sent (deal_id, channel, sent_at, attempts) VALUES (?, ?, ?, 1)
		ON CONFLICT (deal_id, channel) DO UPDATE SET attempts = attempts + 1,
			sent_at = COALESCE(excluded.sent_at, notifications_sent.sent_at)`, dealID, channel, sentAt)
	return err
}

func (s *Store) PendingNotifications(ctx context.Context, maxAttempts int) ([]PendingNotification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT deal_id, channel FROM notifications_sent
		WHERE sent_at IS NULL AND attempts < ? ORDER BY deal_id, channel`, maxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingNotification
	for rows.Next() {
		var p PendingNotification
		if err := rows.Scan(&p.DealID, &p.Channel); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
