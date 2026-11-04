package store

import (
	"errors"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func passVerdict(t *testing.T, s *Store, id, decision string) {
	t.Helper()
	if err := s.SaveVerdict(ctx, Verdict{DealID: id, Decision: decision, Reasons: []string{}, DecidedAt: t0}); err != nil {
		t.Fatal(err)
	}
}

func mustSaveOpinion(t *testing.T, s *Store, o OpinionRecord) {
	t.Helper()
	if err := s.SaveOpinion(ctx, o); err != nil {
		t.Fatal(err)
	}
}

func candidateIDs(t *testing.T, s *Store, now time.Time, limit int) []string {
	t.Helper()
	recs, err := s.JudgeCandidates(ctx, now.Add(-24*time.Hour), now.Add(-10*time.Minute), 3, limit)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range recs {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestJudgeCandidates(t *testing.T) {
	s := openTest(t)
	old := mustUpsert(t, s, deal("old", nil), source.FeedTrending, "a", t0.Add(-48*time.Hour))
	a := mustUpsert(t, s, deal("a", nil), source.FeedTrending, "b", t0.Add(-2*time.Hour))
	b := mustUpsert(t, s, deal("b", nil), source.FeedTrending, "c", t0.Add(-1*time.Hour))
	blocked := mustUpsert(t, s, deal("blocked", nil), source.FeedTrending, "d", t0)
	passVerdict(t, s, old.ID, "pass")
	passVerdict(t, s, a.ID, "pass")
	passVerdict(t, s, b.ID, "pass")
	passVerdict(t, s, blocked.ID, "block")

	got := candidateIDs(t, s, t0, 10)
	if len(got) != 2 || got[0] != b.ID || got[1] != a.ID {
		t.Fatalf("candidates = %v, want [b a] (newest first, passed, seen in 24 h)", got)
	}
	if got := candidateIDs(t, s, t0, 1); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("limit 1 = %v", got)
	}

	// An ok opinion removes the deal; a fresh failure waits 10 minutes.
	mustSaveOpinion(t, s, OpinionRecord{DealID: b.ID, Status: OpinionOK, Rating: "good", Reason: "fine", Model: "m", JudgedAt: t0})
	mustSaveOpinion(t, s, OpinionRecord{DealID: a.ID, Status: OpinionFailed, Error: "HTTP 502", Model: "m", JudgedAt: t0})
	if got := candidateIDs(t, s, t0.Add(5*time.Minute), 10); len(got) != 0 {
		t.Fatalf("right after a failure = %v, want none", got)
	}
	if got := candidateIDs(t, s, t0.Add(11*time.Minute), 10); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("after 10 min = %v, want [a]", got)
	}
	// Three failed attempts: given up.
	for i := 0; i < 2; i++ {
		mustSaveOpinion(t, s, OpinionRecord{DealID: a.ID, Status: OpinionFailed, Error: "HTTP 502", JudgedAt: t0})
	}
	o, err := s.GetOpinion(ctx, a.ID)
	if err != nil || o.Attempts != 3 {
		t.Fatalf("attempts = %d, %v", o.Attempts, err)
	}
	if got := candidateIDs(t, s, t0.Add(time.Hour), 10); len(got) != 0 {
		t.Fatalf("after 3 attempts = %v, want none", got)
	}
}

func TestSaveOpinionOKClearsError(t *testing.T) {
	s := openTest(t)
	a := mustUpsert(t, s, deal("a", nil), source.FeedTrending, "a", t0)
	mustSaveOpinion(t, s, OpinionRecord{DealID: a.ID, Status: OpinionFailed, Error: "timed out", JudgedAt: t0})
	mustSaveOpinion(t, s, OpinionRecord{DealID: a.ID, Status: OpinionOK, Rating: "meh", Reason: "ok", Model: "m", JudgedAt: t0.Add(time.Minute)})
	o, err := s.GetOpinion(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := OpinionRecord{DealID: a.ID, Status: OpinionOK, Rating: "meh", Reason: "ok", Model: "m", Attempts: 2, JudgedAt: t0.Add(time.Minute)}
	if o != want {
		t.Fatalf("opinion = %+v, want %+v", o, want)
	}
	if _, err := s.GetOpinion(ctx, "mydealz:none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing opinion err = %v", err)
	}
}

func TestOpinionsOnlyOK(t *testing.T) {
	s := openTest(t)
	a := mustUpsert(t, s, deal("a", nil), source.FeedTrending, "a", t0)
	b := mustUpsert(t, s, deal("b", nil), source.FeedTrending, "b", t0)
	mustSaveOpinion(t, s, OpinionRecord{DealID: a.ID, Status: OpinionOK, Rating: "bad", Reason: "fees", Model: "m", JudgedAt: t0})
	mustSaveOpinion(t, s, OpinionRecord{DealID: b.ID, Status: OpinionFailed, Error: "x", JudgedAt: t0})
	got, err := s.Opinions(ctx, []string{a.ID, b.ID, "mydealz:none"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[a.ID].Reason != "fees" {
		t.Fatalf("opinions = %+v", got)
	}
	if got, err := s.Opinions(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("no ids = %v, %v", got, err)
	}
}

func TestJudgeCallsAndPrune(t *testing.T) {
	s := openTest(t)
	for _, at := range []time.Time{t0.Add(-72 * time.Hour), t0.Add(-25 * time.Hour), t0.Add(-time.Hour), t0} {
		if err := s.RecordJudgeCall(ctx, at); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.JudgeCallsSince(ctx, t0.Add(-24*time.Hour)); err != nil || n != 2 {
		t.Fatalf("calls in 24 h = %d, %v", n, err)
	}
	a := mustUpsert(t, s, deal("a", nil), source.FeedTrending, "a", t0.Add(-200*24*time.Hour))
	mustSaveOpinion(t, s, OpinionRecord{DealID: a.ID, Status: OpinionOK, Rating: "good", Reason: "x", JudgedAt: t0})
	if _, err := s.Prune(ctx, t0.Add(-180*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOpinion(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("opinion survived its deal's prune: %v", err)
	}
	// Old judge calls go separately; the runner prunes them (Task 4).
	if _, err := s.PruneJudgeCalls(ctx, t0.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.JudgeCallsSince(ctx, time.Time{}); n != 3 {
		t.Errorf("calls after prune = %d, want 3", n)
	}
}
