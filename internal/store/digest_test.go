package store

import (
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func TestDigestCutoff(t *testing.T) {
	s := openTest(t)
	if _, ok, err := s.DigestCutoff(ctx, "phone"); ok || err != nil {
		t.Fatalf("fresh cutoff = %v, %v", ok, err)
	}
	for _, at := range []time.Time{t0, t0.Add(24 * time.Hour)} {
		if err := s.SetDigestCutoff(ctx, "phone", at); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.DigestCutoff(ctx, "phone")
	if !ok || err != nil || !got.Equal(t0.Add(24*time.Hour)) {
		t.Errorf("cutoff = %v, %v, %v", got, ok, err)
	}
	if _, ok, _ := s.DigestCutoff(ctx, "tg"); ok {
		t.Error("cutoffs are per channel")
	}
}

func dealIDs(recs []DealRecord) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}

func TestDigestDeals(t *testing.T) {
	s := openTest(t)
	add := func(id string, temp *int, at time.Time, decision string, watch bool) {
		mustUpsert(t, s, deal(id, temp), source.FeedTrending, "shop|"+id, at)
		save(t, s, "mydealz:"+id, decision, 0, watch)
	}
	since := t0.Add(-24 * time.Hour)
	add("stale", intp(900), since, "pass", false)                 // last seen at since: excluded (exclusive)
	add("edge", intp(100), since.Add(time.Second), "pass", false) // last seen just after since: included
	add("hot", intp(500), t0.Add(-2*time.Hour), "pass", false)    // top 1
	add("warm", intp(300), t0.Add(-3*time.Hour), "pass", false)   // top 3
	add("cold", intp(800), t0.Add(-4*time.Hour), "block", false)  // blocked: excluded
	add("nil", nil, t0.Add(-time.Hour), "pass", false)            // unknown temperature: last
	add("w-old", intp(10), t0.Add(-5*time.Hour), "pass", true)    // watchlist, older
	add("w-new", intp(10), t0.Add(-30*time.Minute), "pass", true) // watchlist, newer
	// First seen long before since while still cold, passing and seen again since:
	// it trended after the previous digest and belongs in this one.
	add("trended", intp(400), since.Add(-48*time.Hour), "pass", false)
	mustUpsert(t, s, deal("trended", intp(400)), source.FeedTrending, "shop|trended", t0.Add(-time.Hour))

	hits, others, err := s.DigestDeals(ctx, "phone", nil, since, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := dealIDs(hits); !slices.Equal(got, []string{"mydealz:w-new", "mydealz:w-old"}) {
		t.Errorf("hits = %v", got)
	}
	if got := dealIDs(others); !slices.Equal(got, []string{"mydealz:hot", "mydealz:trended", "mydealz:warm"}) {
		t.Errorf("others = %v", got)
	}
	_, all, _ := s.DigestDeals(ctx, "phone", nil, since, 25)
	if got := dealIDs(all); !slices.Equal(got, []string{"mydealz:hot", "mydealz:trended", "mydealz:warm", "mydealz:edge", "mydealz:nil"}) {
		t.Errorf("all others = %v", got)
	}
}

func TestDigestDealsSkipsDealsAlreadySentToTheChannel(t *testing.T) {
	s := openTest(t)
	for _, id := range []string{"a", "b"} {
		mustUpsert(t, s, deal(id, intp(100)), source.FeedTrending, "shop|"+id, t0)
		save(t, s, "mydealz:"+id, "pass", 0, false)
	}
	if err := s.CompleteDigest(ctx, "phone", []string{"mydealz:a"}, t0); err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, s, deal("a", intp(100)), source.FeedTrending, "shop|a", t0.Add(time.Hour)) // still active
	since := t0.Add(-time.Hour)
	if _, others, err := s.DigestDeals(ctx, "phone", nil, since, 10); err != nil || !slices.Equal(dealIDs(others), []string{"mydealz:b"}) {
		t.Errorf("phone = %v, %v", dealIDs(others), err)
	}
	if _, others, err := s.DigestDeals(ctx, "tg", nil, since, 10); err != nil || len(others) != 2 {
		t.Errorf("tg = %v, %v; want both", dealIDs(others), err)
	}
}

func TestDigestDealsCapsWatchlistHits(t *testing.T) {
	s := openTest(t)
	for i := range MaxDigestHits + 2 {
		id := string(rune('a' + i))
		mustUpsert(t, s, deal(id, nil), source.FeedTrending, "shop|"+id, t0.Add(-time.Duration(i)*time.Minute))
		save(t, s, "mydealz:"+id, "pass", 0, true)
	}
	hits, _, err := s.DigestDeals(ctx, "phone", nil, t0.Add(-time.Hour), 10)
	if err != nil || len(hits) != MaxDigestHits {
		t.Errorf("hits = %d, %v", len(hits), err)
	}
}

func sentRows(t *testing.T, s *Store, channel string) []string {
	t.Helper()
	rows, err := s.db.QueryContext(ctx, `SELECT deal_id FROM digest_sent WHERE channel = ? ORDER BY deal_id`, channel)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func TestCompleteDigest(t *testing.T) {
	s := openTest(t)
	for _, id := range []string{"a", "b"} {
		mustUpsert(t, s, deal(id, intp(100)), source.FeedTrending, "shop|"+id, t0)
	}
	ids := []string{"mydealz:a", "mydealz:b", "mydealz:a"}
	for _, at := range []time.Time{t0, t0.Add(24 * time.Hour)} {
		if err := s.CompleteDigest(ctx, "phone", ids, at); err != nil {
			t.Fatalf("complete at %v: %v", at, err)
		}
	}
	if got := sentRows(t, s, "phone"); !slices.Equal(got, []string{"mydealz:a", "mydealz:b"}) {
		t.Errorf("sent = %v", got)
	}
	if got := sentRows(t, s, "tg"); len(got) != 0 {
		t.Errorf("tg sent = %v", got)
	}
	if last, ok, err := s.DigestCutoff(ctx, "phone"); !ok || err != nil || !last.Equal(t0.Add(24*time.Hour)) {
		t.Errorf("cutoff = %v, %v, %v", last, ok, err)
	}
}

func TestPruneRemovesDigestSent(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("old", intp(10)), source.FeedTrending, "a", t0.Add(-200*24*time.Hour))
	mustUpsert(t, s, deal("new", intp(10)), source.FeedTrending, "b", t0)
	if err := s.CompleteDigest(ctx, "phone", []string{"mydealz:old", "mydealz:new"}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(ctx, t0.Add(-180*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := sentRows(t, s, "phone"); !slices.Equal(got, []string{"mydealz:new"}) {
		t.Errorf("sent after prune = %v", got)
	}
}
