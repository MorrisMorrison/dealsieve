package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	OpinionOK     = "ok"
	OpinionFailed = "failed"
)

type OpinionRecord struct {
	DealID, Status, Rating, Reason, Model, Error string
	Attempts                                     int
	JudgedAt                                     time.Time
}

const opinionColumns = `deal_id, status, rating, reason, model, error, attempts, judged_at`

func scanOpinion(sc scanner) (OpinionRecord, error) {
	var o OpinionRecord
	var at int64
	err := sc.Scan(&o.DealID, &o.Status, &o.Rating, &o.Reason, &o.Model, &o.Error, &o.Attempts, &at)
	o.JudgedAt = fromUnix(at)
	return o, err
}

// JudgeCandidates returns passed deals seen since seenSince that have no
// opinion yet, or a failed one with fewer than maxAttempts attempts made
// before retryBefore; newest first.
func (s *Store) JudgeCandidates(ctx context.Context, seenSince, retryBefore time.Time, maxAttempts, limit int) ([]DealRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dealColumns+`
		FROM deals d JOIN verdicts v ON v.deal_id = d.id
		LEFT JOIN opinions o ON o.deal_id = d.id
		WHERE v.decision = 'pass' AND d.last_seen >= ?
			AND (o.deal_id IS NULL OR (o.status = 'failed' AND o.attempts < ? AND o.judged_at <= ?))
		ORDER BY d.first_seen DESC, d.id LIMIT ?`,
		seenSince.Unix(), maxAttempts, retryBefore.Unix(), limit)
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

// SaveOpinion stores the result of one judge call; every call counts as an
// attempt. A success clears the earlier error.
func (s *Store) SaveOpinion(ctx context.Context, o OpinionRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO opinions (`+opinionColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT (deal_id) DO UPDATE SET status = excluded.status, rating = excluded.rating,
			reason = excluded.reason, model = excluded.model, error = excluded.error,
			attempts = opinions.attempts + 1, judged_at = excluded.judged_at`,
		o.DealID, o.Status, o.Rating, o.Reason, o.Model, o.Error, o.JudgedAt.Unix())
	return err
}

func (s *Store) GetOpinion(ctx context.Context, dealID string) (OpinionRecord, error) {
	o, err := scanOpinion(s.db.QueryRowContext(ctx, `SELECT `+opinionColumns+` FROM opinions WHERE deal_id = ?`, dealID))
	if errors.Is(err, sql.ErrNoRows) {
		return OpinionRecord{}, ErrNotFound
	}
	return o, err
}

// Opinions returns the successful opinions for ids, keyed by deal id.
func (s *Store) Opinions(ctx context.Context, ids []string) (map[string]OpinionRecord, error) {
	out := map[string]OpinionRecord{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, OpinionOK)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+opinionColumns+` FROM opinions
		WHERE status = ? AND deal_id IN (?`+strings.Repeat(", ?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		o, err := scanOpinion(rows)
		if err != nil {
			return nil, err
		}
		out[o.DealID] = o
	}
	return out, rows.Err()
}

func (s *Store) RecordJudgeCall(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO judge_calls (at) VALUES (?)`, at.Unix())
	return err
}

func (s *Store) JudgeCallsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM judge_calls WHERE at >= ?`, since.Unix()).Scan(&n)
	return n, err
}

func (s *Store) PruneJudgeCalls(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM judge_calls WHERE at < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
