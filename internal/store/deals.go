package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

type DealRecord struct {
	source.Deal
	TitleClean  string
	Fingerprint string
	FirstSeen   time.Time
	LastSeen    time.Time
	InTrending  bool
	InNew       bool
}

var feedColumn = map[source.Feed]string{
	source.FeedTrending: "in_trending",
	source.FeedNew:      "in_new",
}

const dealColumns = `d.id, d.source, d.url, d.title, d.title_clean, d.merchant, d.price, d.category,
	d.image_url, d.fingerprint, d.first_seen, d.last_seen, d.in_trending, d.in_new, d.temperature`

type scanner interface{ Scan(dest ...any) error }

func scanDeal(sc scanner, extra ...any) (DealRecord, error) {
	var r DealRecord
	var first, last int64
	var temp sql.NullInt64
	dest := append([]any{&r.ID, &r.Source, &r.URL, &r.Title, &r.TitleClean, &r.Merchant, &r.Price,
		&r.Category, &r.ImageURL, &r.Fingerprint, &first, &last, &r.InTrending, &r.InNew, &temp}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return DealRecord{}, err
	}
	r.FirstSeen, r.LastSeen = fromUnix(first), fromUnix(last)
	if temp.Valid {
		t := int(temp.Int64)
		r.Temperature = &t
	}
	return r, nil
}

// UpsertDeal records that feed showed d at now. first_seen never changes; a
// missing temperature keeps the last known one.
func (s *Store) UpsertDeal(ctx context.Context, d source.Deal, feed source.Feed, titleClean, fingerprint string, now time.Time) (DealRecord, error) {
	col, ok := feedColumn[feed]
	if !ok {
		return DealRecord{}, fmt.Errorf("unknown feed %q", feed)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DealRecord{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO deals (id, source, url, title, title_clean, merchant, price,
			category, image_url, fingerprint, first_seen, last_seen, temperature, `+col+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT (id) DO UPDATE SET url = excluded.url, title = excluded.title,
			title_clean = excluded.title_clean, merchant = excluded.merchant, price = excluded.price,
			category = excluded.category, image_url = excluded.image_url,
			fingerprint = excluded.fingerprint, last_seen = excluded.last_seen,
			temperature = COALESCE(excluded.temperature, deals.temperature), `+col+` = 1`,
		d.ID, d.Source, d.URL, d.Title, titleClean, d.Merchant, d.Price, d.Category, d.ImageURL,
		fingerprint, now.Unix(), now.Unix(), nullInt(d.Temperature))
	if err != nil {
		return DealRecord{}, err
	}
	if d.Temperature != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO temperature_snapshots (deal_id, at, temperature) VALUES (?, ?, ?)`,
			d.ID, now.Unix(), *d.Temperature); err != nil {
			return DealRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return DealRecord{}, err
	}
	return s.GetDeal(ctx, d.ID)
}

func (s *Store) GetDeal(ctx context.Context, id string) (DealRecord, error) {
	rec, err := scanDeal(s.db.QueryRowContext(ctx, `SELECT `+dealColumns+` FROM deals d WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DealRecord{}, ErrNotFound
	}
	return rec, err
}

func (s *Store) DealsSeenSince(ctx context.Context, since time.Time) ([]DealRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dealColumns+` FROM deals d WHERE d.last_seen >= ? ORDER BY d.first_seen, d.id`, since.Unix())
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

// CountRecurrence counts the deals with this fingerprint first seen before
// the deal id (first seen at firstSeen), within window before it, and returns
// when the latest of them was first seen. Deals first seen in the same poll
// are ordered by id, so of two identical ones only the second counts the
// first. Looking only backwards keeps a verdict stable: re-posts that arrive
// later never turn an earlier deal into a recurring one. Only deals of the
// same source count: sites don't share offers.
func (s *Store) CountRecurrence(ctx context.Context, source, fingerprint, id string, firstSeen time.Time, window time.Duration) (int, time.Time, error) {
	var n int
	var last sql.NullInt64
	at := firstSeen.Unix()
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(first_seen) FROM deals
		WHERE source = ? AND fingerprint = ? AND first_seen >= ? AND (first_seen < ? OR (first_seen = ? AND id < ?))`,
		source, fingerprint, firstSeen.Add(-window).Unix(), at, at, id).Scan(&n, &last)
	if err != nil || !last.Valid {
		return n, time.Time{}, err
	}
	return n, fromUnix(last.Int64), nil
}

// Rekey recomputes every deal's cleaned title and fingerprint with key and
// moves each junk action to a current fingerprint: the new key of the latest
// deal that had the old key and was seen before the click (the deal the
// click most likely came from), or, when no stored deal had the old key any
// more, rekeyOld of the key itself. A moved junk action that would duplicate
// an existing one is dropped. It returns how many deals changed; with
// unchanged fingerprint rules it changes nothing.
func (s *Store) Rekey(ctx context.Context, key func(merchant, title string) (clean, fingerprint string), rekeyOld func(fingerprint string) string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	type change struct{ id, clean, fp string }
	var changes []change
	moved := map[string][]sighting{} // old fingerprint -> its deals' new fingerprints
	rows, err := tx.QueryContext(ctx, `SELECT id, title, merchant, title_clean, fingerprint, first_seen FROM deals`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id, title, merchant, oldClean, oldFP string
		var firstSeen int64
		if err := rows.Scan(&id, &title, &merchant, &oldClean, &oldFP, &firstSeen); err != nil {
			rows.Close()
			return 0, err
		}
		clean, fp := key(merchant, title)
		moved[oldFP] = append(moved[oldFP], sighting{firstSeen, fp})
		if clean != oldClean || fp != oldFP {
			changes = append(changes, change{id, clean, fp})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `UPDATE deals SET title_clean = ?, fingerprint = ? WHERE id = ?`, c.clean, c.fp, c.id); err != nil {
			return 0, err
		}
	}

	type junk struct {
		id      int64
		value   string
		created int64
	}
	var junks []junk
	rows, err = tx.QueryContext(ctx, `SELECT id, value, created_at FROM actions WHERE kind = ? ORDER BY id`, ActionJunk)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var j junk
		if err := rows.Scan(&j.id, &j.value, &j.created); err != nil {
			rows.Close()
			return 0, err
		}
		junks = append(junks, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, j := range junks {
		have[j.value] = true
	}
	for _, j := range junks {
		next := rekeyOld(j.value)
		if sightings := moved[j.value]; len(sightings) > 0 {
			next = latestBefore(sightings, j.created)
		}
		if next == j.value {
			continue
		}
		if have[next] {
			_, err = tx.ExecContext(ctx, `DELETE FROM actions WHERE id = ?`, j.id)
		} else {
			have[next] = true
			_, err = tx.ExecContext(ctx, `UPDATE actions SET value = ? WHERE id = ?`, next, j.id)
		}
		if err != nil {
			return 0, err
		}
	}
	return len(changes), tx.Commit()
}

// sighting is one deal's first sighting and its new fingerprint, for Rekey.
type sighting struct {
	firstSeen int64
	fp        string
}

// latestBefore returns the fingerprint of the latest sighting at or before
// at, or of the earliest one if all came after it.
func latestBefore(sightings []sighting, at int64) string {
	best, earliest := -1, 0
	for i, x := range sightings {
		if x.firstSeen < sightings[earliest].firstSeen {
			earliest = i
		}
		if x.firstSeen <= at && (best < 0 || x.firstSeen > sightings[best].firstSeen) {
			best = i
		}
	}
	if best < 0 {
		best = earliest
	}
	return sightings[best].fp
}

// Temperatures returns each deal's snapshots, oldest first.
func (s *Store) Temperatures(ctx context.Context, ids []string) (map[string][]int, error) {
	out := make(map[string][]int, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT deal_id, temperature FROM temperature_snapshots
		WHERE deal_id IN (?`+strings.Repeat(", ?", len(ids)-1)+`) ORDER BY deal_id, at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t int
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = append(out[id], t)
	}
	return out, rows.Err()
}

// Prune deletes deals last seen before before, with everything attached.
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM deals WHERE last_seen < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
