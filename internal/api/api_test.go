package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/poller"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

type fakeRecompute struct{ calls int }

func (f *fakeRecompute) RecomputeRecent(context.Context) error { f.calls++; return nil }

func intp(v int) *int { return &v }

type harness struct {
	srv *httptest.Server
	st  *store.Store
	rc  *fakeRecompute
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	seed := func(id, title, category string, temp int, at time.Time, decision string, reasons ...string) {
		d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
			Title: title, Merchant: "Example Shop", Price: "9€", Category: category, Temperature: intp(temp)}
		if _, err := st.UpsertDeal(ctx, d, source.FeedTrending, strings.ToLower(title), "shop|"+id, at); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveVerdict(ctx, store.Verdict{DealID: d.ID, Decision: decision, Score: temp, Reasons: reasons, DecidedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	seed("a", "Example OLED TV", "Elektronik", 150, now.Add(-time.Hour), "pass")
	seed("b", "Example Kabel", "", 80, now.Add(-time.Hour), "block", "too cold: 80° < 100°")
	seed("old", "Example Old", "Elektronik", 500, now.Add(-5*24*time.Hour), "pass")

	rc := &fakeRecompute{}
	s := &Server{
		Store:     st,
		Recompute: rc,
		Status:    func() []poller.Status { return []poller.Status{{Feed: "trending", Interval: "5m0s"}} },
		Config:    &config.Config{Watchlist: []string{"oled"}, Rules: config.Rules{MinTemperature: 100}},
		Assets:    fstest.MapFS{"index.html": {Data: []byte("<h1>dealsieve</h1>")}},
		Log:       slog.New(slog.DiscardHandler),
		Now:       func() time.Time { return now },
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &harness{srv: srv, st: st, rc: rc}
}

func (h *harness) do(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

type dealsResponse struct {
	Deals []struct {
		ID           string   `json:"id"`
		Title        string   `json:"title"`
		Temperature  *int     `json:"temperature"`
		Temperatures []int    `json:"temperatures"`
		Decision     string   `json:"decision"`
		Reasons      []string `json:"reasons"`
		InTrending   bool     `json:"in_trending"`
	} `json:"deals"`
	Counts map[string]int `json:"counts"`
}

func TestListDeals(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/api/deals", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got dealsResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Deals) != 1 {
		t.Fatalf("deals = %+v, want only the recent pass", got.Deals)
	}
	d := got.Deals[0]
	if d.ID != "mydealz:a" || *d.Temperature != 150 || !slices.Equal(d.Temperatures, []int{150}) ||
		d.Decision != "pass" || d.Reasons == nil || !d.InTrending {
		t.Errorf("deal = %+v", d)
	}

	// Counts cover every tab over the same window, whichever view is listed.
	wantCounts := map[string]int{"passed": 1, "watchlist": 0, "blocked": 1}
	if !maps.Equal(got.Counts, wantCounts) {
		t.Errorf("counts = %v, want %v", got.Counts, wantCounts)
	}

	_, body = h.do(t, "GET", "/api/deals?view=blocked", "")
	got = dealsResponse{}
	json.Unmarshal(body, &got)
	if !maps.Equal(got.Counts, wantCounts) {
		t.Errorf("blocked view counts = %v, want %v", got.Counts, wantCounts)
	}
	if len(got.Deals) != 1 || got.Deals[0].Reasons[0] != "too cold: 80° < 100°" {
		t.Errorf("blocked = %+v", got.Deals)
	}
	if resp, _ := h.do(t, "GET", "/api/deals?view=nope", ""); resp.StatusCode != 400 {
		t.Errorf("unknown view status = %d", resp.StatusCode)
	}
}

func TestAddAndDeleteAction(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "POST", "/api/actions", `{"kind":"mute_category","deal_id":"mydealz:a"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var a store.Action
	json.Unmarshal(body, &a)
	if a.Kind != store.ActionMuteCategory || a.Value != "Elektronik" || a.ID == 0 || h.rc.calls != 1 {
		t.Errorf("action %+v, recompute calls %d", a, h.rc.calls)
	}

	if resp, _ := h.do(t, "POST", "/api/actions", `{"kind":"junk","deal_id":"mydealz:a"}`); resp.StatusCode != 201 {
		t.Errorf("junk status = %d", resp.StatusCode)
	}
	actions, _ := h.st.Actions(context.Background())
	if actions[0].Value != "shop|a" {
		t.Errorf("junk value = %q, want the fingerprint", actions[0].Value)
	}

	if resp, _ := h.do(t, "DELETE", "/api/actions/"+jsonID(a.ID), ""); resp.StatusCode != 204 {
		t.Errorf("delete status = %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, "DELETE", "/api/actions/"+jsonID(a.ID), ""); resp.StatusCode != 404 {
		t.Errorf("second delete status = %d", resp.StatusCode)
	}
	if h.rc.calls != 3 {
		t.Errorf("recompute calls = %d, want 3", h.rc.calls)
	}
}

func jsonID(id int64) string { b, _ := json.Marshal(id); return string(b) }

func TestActionErrors(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		body string
		want int
	}{
		{`not json`, 400},
		{`{"kind":"explode","deal_id":"mydealz:a"}`, 400},
		{`{"kind":"hide_deal","deal_id":"mydealz:404"}`, 404},
		{`{"kind":"mute_category","deal_id":"mydealz:b"}`, 400}, // deal b has no category
	}
	for _, tt := range tests {
		if resp, body := h.do(t, "POST", "/api/actions", tt.body); resp.StatusCode != tt.want || !strings.Contains(string(body), `"error"`) {
			t.Errorf("%s: status %d (%s), want %d", tt.body, resp.StatusCode, body, tt.want)
		}
	}
	if resp, _ := h.do(t, "DELETE", "/api/actions/abc", ""); resp.StatusCode != 400 {
		t.Errorf("bad id status = %d", resp.StatusCode)
	}
	if h.rc.calls != 0 {
		t.Errorf("recompute ran %d times on errors", h.rc.calls)
	}
}

func TestRulesStatusHealthAndPage(t *testing.T) {
	h := newHarness(t)
	h.do(t, "POST", "/api/actions", `{"kind":"hide_deal","deal_id":"mydealz:a"}`)

	_, body := h.do(t, "GET", "/api/rules", "")
	var rules struct {
		Rules     map[string]any `json:"rules"`
		Watchlist []string       `json:"watchlist"`
		Actions   []store.Action `json:"actions"`
	}
	if err := json.Unmarshal(body, &rules); err != nil {
		t.Fatal(err)
	}
	if rules.Rules["min_temperature"] != float64(100) || rules.Watchlist[0] != "oled" || len(rules.Actions) != 1 {
		t.Errorf("rules = %s", body)
	}

	_, body = h.do(t, "GET", "/api/status", "")
	if !strings.Contains(string(body), `"feed":"trending"`) {
		t.Errorf("status = %s", body)
	}
	if resp, body := h.do(t, "GET", "/health", ""); resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("health = %d %q", resp.StatusCode, body)
	}
	if _, body := h.do(t, "GET", "/", ""); !strings.Contains(string(body), "dealsieve") {
		t.Errorf("page = %q", body)
	}
}

func TestCrossOriginWritesAreRejected(t *testing.T) {
	h := newHarness(t)
	post := func(site string) int {
		req, err := http.NewRequest("POST", h.srv.URL+"/api/actions", strings.NewReader(`{"kind":"hide_deal","deal_id":"mydealz:a"}`))
		if err != nil {
			t.Fatal(err)
		}
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		resp, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("cross-site"); got != 403 {
		t.Errorf("cross-site status = %d, want 403", got)
	}
	actions, err := h.st.Actions(context.Background())
	if err != nil || len(actions) != 0 {
		t.Fatalf("actions after rejected request = %v, %v", actions, err)
	}
	for _, site := range []string{"", "same-origin"} {
		if got := post(site); got != 201 {
			t.Errorf("Sec-Fetch-Site %q status = %d, want 201", site, got)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/", "/api/status", "/health", "/nope"} {
		resp, _ := h.do(t, "GET", path, "")
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s headers = %v", path, resp.Header)
		}
	}
}
