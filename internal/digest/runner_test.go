package digest

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

var ctx = context.Background()

type fakeNotifier struct {
	mu   sync.Mutex
	sent []notify.Message
	fail bool
}

func (f *fakeNotifier) Send(_ context.Context, m notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("boom")
	}
	f.sent = append(f.sent, m)
	return nil
}

type runnerHarness struct {
	r     *Runner
	st    *store.Store
	now   *time.Time
	s     *settings.Settings
	phone *fakeNotifier
	tg    *fakeNotifier
}

func newRunner(t *testing.T) *runnerHarness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := at("2026-11-04", "18:00").UTC()
	s := settings.Defaults()
	s.Channels = map[string]settings.Channel{
		"phone": {Type: "ntfy", URL: "https://ntfy.example.com", Topic: "deals"},
		"tg":    {Type: "telegram", ChatID: "1"},
	}
	s.Digest = settings.Digest{Enabled: true, Time: "18:00", Timezone: "Europe/Berlin", Top: 10, Channels: []string{"phone", "tg"}}
	h := &runnerHarness{st: st, now: &now, s: &s, phone: &fakeNotifier{}, tg: &fakeNotifier{}}
	h.r = &Runner{
		Store:    st,
		Settings: func() settings.Settings { return *h.s },
		Env:      config.Env{TelegramToken: "x", PublicURL: "https://deals.example.com"},
		Notifiers: func(map[string]settings.Channel) map[string]notify.Notifier {
			return map[string]notify.Notifier{"phone": h.phone, "tg": h.tg}
		},
		Now: func() time.Time { return *h.now },
		Log: slog.New(slog.DiscardHandler),
	}
	return h
}

// seedFrom is seed for a deal of another source.
func (h *runnerHarness) seedFrom(t *testing.T, src, id string, temp int, firstSeen time.Time) {
	t.Helper()
	d := source.Deal{ID: src + ":" + id, Source: src, URL: "https://d.example.test/" + id,
		Title: "Deal " + id, Merchant: "Example Shop", Price: "9€", Temperature: &temp}
	if _, err := h.st.UpsertDeal(ctx, d, source.FeedTrending, "deal", src+"|"+id, firstSeen); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SaveVerdict(ctx, store.Verdict{DealID: d.ID, Decision: "pass", Score: temp, DecidedAt: firstSeen}); err != nil {
		t.Fatal(err)
	}
}

func (h *runnerHarness) seed(t *testing.T, id string, temp int, firstSeen time.Time, watch bool) {
	t.Helper()
	h.observe(t, id, temp, firstSeen, "pass", watch)
}

// observe records that a feed showed the deal at `at` with the given verdict:
// the first call sets first_seen, every call moves last_seen.
func (h *runnerHarness) observe(t *testing.T, id string, temp int, at time.Time, decision string, watch bool) {
	t.Helper()
	d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
		Title: "Deal " + id, Merchant: "Example Shop", Price: "9€", Temperature: &temp}
	if _, err := h.st.UpsertDeal(ctx, d, source.FeedTrending, "deal", "shop|"+id, at); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SaveVerdict(ctx, store.Verdict{DealID: d.ID, Decision: decision, Score: temp, WatchlistHit: watch, DecidedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func TestTickSendsOncePerDay(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-2*time.Hour), false)
	h.seed(t, "2", 50, h.now.Add(-time.Hour), true)
	h.seed(t, "old", 900, h.now.Add(-30*time.Hour), false) // before the first 24 h window

	h.r.Tick(ctx)
	h.r.Tick(ctx) // same minute: nothing more
	if len(h.phone.sent) != 1 || len(h.tg.sent) != 1 {
		t.Fatalf("sent phone=%d tg=%d, want 1 each", len(h.phone.sent), len(h.tg.sent))
	}
	m := h.phone.sent[0]
	if m.Title != "dealsieve · 2 new deals" || !strings.Contains(m.Body, "Deal 2") || strings.Contains(m.Body, "Deal old") {
		t.Errorf("message = %+v", m)
	}

	// Next day: only what is new since the previous digest.
	*h.now = h.now.Add(24 * time.Hour)
	h.seed(t, "3", 200, h.now.Add(-time.Hour), false)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 2 || strings.Contains(h.phone.sent[1].Body, "Deal 1") || !strings.Contains(h.phone.sent[1].Body, "Deal 3") {
		t.Errorf("second digest = %+v", h.phone.sent)
	}
}

func TestTickRetriesOnlyTheFailedChannel(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	h.tg.fail = true
	h.r.Tick(ctx)
	h.tg.fail = false
	*h.now = h.now.Add(time.Minute)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 1 || len(h.tg.sent) != 1 {
		t.Errorf("sent phone=%d tg=%d, want 1 each", len(h.phone.sent), len(h.tg.sent))
	}
}

func TestTickEmptyWindowMovesTheCutoff(t *testing.T) {
	h := newRunner(t)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 0 {
		t.Fatalf("sent %d for an empty window", len(h.phone.sent))
	}
	if last, ok, _ := h.st.DigestCutoff(ctx, "phone"); !ok || !last.Equal(*h.now) {
		t.Errorf("cutoff = %v, %v", last, ok)
	}
}

func TestTickRespectsSettings(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	h.s.Digest.Enabled = false
	h.r.Tick(ctx)
	h.s.Digest.Enabled = true
	*h.now = h.now.Add(-time.Hour) // 17:00 local: not yet
	h.r.Tick(ctx)
	if len(h.phone.sent) != 0 {
		t.Errorf("sent %d", len(h.phone.sent))
	}
}

func TestSendTestDoesNotMoveTheCutoff(t *testing.T) {
	h := newRunner(t)
	*h.now = h.now.Add(-6 * time.Hour) // midday: a real digest isn't due
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	res := h.r.SendTest(ctx)
	if len(res) != 2 || !res[0].OK || !res[1].OK || len(h.phone.sent) != 1 {
		t.Fatalf("results = %+v, sent %d", res, len(h.phone.sent))
	}
	if _, ok, _ := h.st.DigestCutoff(ctx, "phone"); ok {
		t.Error("test send stored a cutoff")
	}
	h.tg.fail = true
	*h.now = h.now.Add(24 * time.Hour) // nothing new in the last 24 h
	res = h.r.SendTest(ctx)
	if !strings.HasPrefix(h.phone.sent[1].Title, "dealsieve · nothing new") || res[1].OK || res[1].Error != "boom" {
		t.Errorf("empty test = %+v / %+v", h.phone.sent[1], res)
	}
}

func TestSendTestWarnsAboutMissingTelegramToken(t *testing.T) {
	h := newRunner(t)
	h.r.Env.TelegramToken = ""
	res := h.r.SendTest(ctx)
	if res[1].Channel != "tg" || res[1].OK || !strings.Contains(res[1].Error, "DEALSIEVE_TELEGRAM_TOKEN") || len(h.tg.sent) != 0 {
		t.Errorf("results = %+v", res)
	}
}

func TestTickListsADealThatPassedAfterThePreviousDigest(t *testing.T) {
	h := newRunner(t)
	h.observe(t, "late", 40, h.now.Add(-2*time.Hour), "block", false) // first seen cold at 16:00
	h.r.Tick(ctx)                                                     // day 1, 18:00: empty, cutoff moves
	if len(h.phone.sent) != 0 {
		t.Fatalf("day 1 sent %d", len(h.phone.sent))
	}
	h.observe(t, "late", 400, h.now.Add(time.Hour), "pass", false) // trends at 19:00
	*h.now = h.now.Add(24 * time.Hour)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 1 || !strings.Contains(h.phone.sent[0].Body, "Deal late") {
		t.Errorf("day 2 digest = %+v", h.phone.sent)
	}
}

func TestTickDoesNotRepeatADealStillActive(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 1 {
		t.Fatalf("day 1 sent %d", len(h.phone.sent))
	}
	h.seed(t, "1", 350, h.now.Add(16*time.Hour), false) // still trending the next morning
	h.seed(t, "2", 200, h.now.Add(17*time.Hour), false)
	*h.now = h.now.Add(24 * time.Hour)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 2 || strings.Contains(h.phone.sent[1].Body, "Deal 1") || !strings.Contains(h.phone.sent[1].Body, "Deal 2") {
		t.Errorf("day 2 digest = %+v", h.phone.sent)
	}
}

func TestSendTestDoesNotMarkDealsSent(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	h.r.SendTest(ctx)
	h.r.Tick(ctx)
	if len(h.phone.sent) != 2 || !strings.Contains(h.phone.sent[1].Body, "Deal 1") {
		t.Errorf("digest after a test send = %+v", h.phone.sent)
	}
}

func TestTickWarnsOncePerDayAboutAChannelThatCantSend(t *testing.T) {
	h := newRunner(t)
	var buf bytes.Buffer
	h.r.Log = slog.New(slog.NewTextHandler(&buf, nil))
	h.r.Notifiers = func(map[string]settings.Channel) map[string]notify.Notifier {
		return map[string]notify.Notifier{"phone": h.phone} // tg has no sender
	}
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	for range 3 {
		h.r.Tick(ctx)
		*h.now = h.now.Add(time.Minute)
	}
	if n := strings.Count(buf.String(), "digest channel can't send"); n != 1 {
		t.Errorf("warned %d times on day 1, want 1: %s", n, buf.String())
	}
	*h.now = h.now.Add(24 * time.Hour)
	h.seed(t, "2", 300, h.now.Add(-time.Hour), false)
	h.r.Tick(ctx)
	if n := strings.Count(buf.String(), "digest channel can't send"); n != 2 {
		t.Errorf("warned %d times after day 2, want 2", n)
	}
}

func TestTickListsOnlyEnabledSources(t *testing.T) {
	h := newRunner(t)
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	h.seedFrom(t, "hotukdeals", "2", 300, h.now.Add(-time.Hour))
	h.r.Tick(ctx)
	if len(h.phone.sent) != 1 {
		t.Fatalf("sent %d", len(h.phone.sent))
	}
	m := h.phone.sent[0]
	if !strings.Contains(m.Body, "Deal 1") || strings.Contains(m.Body, "Deal 2") {
		t.Errorf("body = %q, want only the mydealz deal", m.Body)
	}
	if strings.Contains(m.Body, "mydealz ·") {
		t.Errorf("body = %q, want no label with one source", m.Body)
	}
}

func TestTickLabelsLinesWithSeveralSources(t *testing.T) {
	h := newRunner(t)
	h.s.Sources["hotukdeals"] = settings.Source{Enabled: true}
	h.seed(t, "1", 300, h.now.Add(-time.Hour), false)
	h.seedFrom(t, "hotukdeals", "2", 200, h.now.Add(-time.Hour))
	h.r.Tick(ctx)
	if len(h.phone.sent) != 1 {
		t.Fatalf("sent %d", len(h.phone.sent))
	}
	body := h.phone.sent[0].Body
	for _, want := range []string{"1. mydealz · Deal 1", "2. hotukdeals · Deal 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
}
