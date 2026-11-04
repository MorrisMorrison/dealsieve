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
	"github.com/MorrisMorrison/dealsieve/internal/judge"
	"github.com/MorrisMorrison/dealsieve/internal/poller"
	"github.com/MorrisMorrison/dealsieve/internal/product"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/settingsvc"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

type fakeRecompute struct {
	calls, applied int
	err            error
	// cancelled counts recomputes that received an already-cancelled context.
	cancelled int
}

// disconnectKey carries the CancelFunc that simulates the client going away.
type disconnectKey struct{}

func (f *fakeRecompute) ApplySettings(settings.Settings) { f.applied++ }

func (f *fakeRecompute) RecomputeRecent(ctx context.Context) (int, error) {
	f.calls++
	// The client disconnects after the click is stored, just as the re-check starts.
	if cancel, ok := ctx.Value(disconnectKey{}).(context.CancelFunc); ok {
		cancel()
	}
	if ctx.Err() != nil {
		f.cancelled++
	}
	if f.err != nil {
		return 0, f.err
	}
	return 7, nil
}

func intp(v int) *int { return &v }

type harness struct {
	srv *httptest.Server
	st  *store.Store
	rc  *fakeRecompute
	svc *settingsvc.Service
	api *Server
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
	svc := &settingsvc.Service{Store: st, Apply: rc, Now: func() time.Time { return now }}
	if err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:     st,
		Recompute: rc,
		Status:    func() []poller.Status { return []poller.Status{{Feed: "trending", Interval: "5m0s"}} },
		Settings:  svc,
		Env:       config.Env{},
		HTTP:      http.DefaultClient,
		Assets:    fstest.MapFS{"index.html": {Data: []byte("<h1>dealsieve</h1>")}},
		Log:       slog.New(slog.DiscardHandler),
		Now:       func() time.Time { return now },
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &harness{srv: srv, st: st, rc: rc, svc: svc, api: s}
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
	var muted struct {
		Kind    string `json:"kind"`
		Value   string `json:"value"`
		Version int64  `json:"version"`
	}
	json.Unmarshal(body, &muted)
	cur := h.svc.Current()
	if muted.Kind != store.ActionMuteCategory || muted.Value != "Elektronik" || muted.Version != cur.Version ||
		!slices.Equal(cur.Settings.Rules.MutedCategories, []string{"Elektronik"}) || h.rc.calls != 1 {
		t.Errorf("mute response %s, settings %+v, recompute calls %d", body, cur.Settings.Rules, h.rc.calls)
	}

	resp, body = h.do(t, "POST", "/api/actions", `{"kind":"junk","deal_id":"mydealz:a"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("junk status = %d", resp.StatusCode)
	}
	var a store.Action
	json.Unmarshal(body, &a)
	actions, _ := h.st.Actions(context.Background())
	if len(actions) != 1 || actions[0].Value != "shop|a" || a.ID == 0 {
		t.Errorf("actions = %+v, want only the junk fingerprint", actions)
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

// serve calls the handler directly with a request whose client disconnects
// right after the click is stored (see fakeRecompute).
func (h *harness) serve(t *testing.T, method, path, body string) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, disconnectKey{}, cancel)
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.api.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func TestRecomputeSurvivesClientDisconnect(t *testing.T) {
	h := newHarness(t)
	if code := h.serve(t, "POST", "/api/actions", `{"kind":"hide_deal","deal_id":"mydealz:a"}`); code != http.StatusCreated {
		t.Fatalf("add = %d", code)
	}
	if code := h.serve(t, "POST", "/api/actions/undo", `{"kind":"hide_deal","deal_id":"mydealz:a"}`); code != http.StatusNoContent {
		t.Fatalf("undo = %d", code)
	}
	a, err := h.st.AddAction(context.Background(), store.ActionHide, "mydealz:a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code := h.serve(t, "DELETE", "/api/actions/"+jsonID(a.ID), ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	if h.rc.calls != 3 || h.rc.cancelled != 0 {
		t.Errorf("recompute calls = %d (want 3), ran with a cancelled context %d times (want 0)", h.rc.calls, h.rc.cancelled)
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

func TestStatusHealthAndPage(t *testing.T) {
	h := newHarness(t)
	_, body := h.do(t, "GET", "/api/status", "")
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

func TestUndoHideAndJunk(t *testing.T) {
	h := newHarness(t)
	h.do(t, "POST", "/api/actions", `{"kind":"hide_deal","deal_id":"mydealz:a"}`)
	h.do(t, "POST", "/api/actions", `{"kind":"junk","deal_id":"mydealz:a"}`)
	for _, kind := range []string{"hide_deal", "junk"} {
		resp, _ := h.do(t, "POST", "/api/actions/undo", `{"kind":"`+kind+`","deal_id":"mydealz:a"}`)
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("undo %s = %d", kind, resp.StatusCode)
		}
	}
	if left, _ := h.st.Actions(context.Background()); len(left) != 0 {
		t.Errorf("actions left = %+v", left)
	}
	if resp, _ := h.do(t, "POST", "/api/actions/undo", `{"kind":"mute_category","deal_id":"mydealz:a"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("undo mute = %d, want 400", resp.StatusCode)
	}
	if resp, _ := h.do(t, "POST", "/api/actions/undo", `{"kind":"junk","deal_id":"mydealz:404"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("undo unknown deal = %d, want 404", resp.StatusCode)
	}
}

type fakeJudgeRunner struct {
	status judge.Status
	got    settings.Judge
	result judge.TestResult
}

func (f *fakeJudgeRunner) Status(context.Context) (judge.Status, error) { return f.status, nil }
func (f *fakeJudgeRunner) Test(_ context.Context, j settings.Judge) judge.TestResult {
	f.got = j
	return f.result
}

func TestListDealsIncludesOpinion(t *testing.T) {
	h := newHarness(t)
	at := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	if err := h.st.SaveOpinion(context.Background(), store.OpinionRecord{DealID: "mydealz:a", Status: "ok",
		Rating: "bad", Reason: "Looks like a re-post.", Model: "example/model", JudgedAt: at}); err != nil {
		t.Fatal(err)
	}
	_, body := h.do(t, "GET", "/api/deals", "")
	var resp struct {
		Deals []map[string]any `json:"deals"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, d := range resp.Deals {
		op, present := d["opinion"]
		if !present {
			t.Fatalf("deal %v has no opinion key", d["id"])
		}
		if d["id"] == "mydealz:a" {
			m, ok := op.(map[string]any)
			if !ok || m["rating"] != "bad" || m["reason"] != "Looks like a re-post." || m["model"] != "example/model" || m["judged_at"] == "" {
				t.Errorf("opinion = %v", op)
			}
			seen++
		} else if op != nil {
			t.Errorf("deal %v opinion = %v, want null", d["id"], op)
		}
	}
	if seen != 1 {
		t.Fatalf("deal a seen %d times in %s", seen, body)
	}
}

func TestStatusIncludesJudge(t *testing.T) {
	h := newHarness(t)
	h.api.Judge = &fakeJudgeRunner{status: judge.Status{Calls24h: 3, LastError: "HTTP 429"}}
	h.api.Env = config.Env{JudgeURL: "https://llm.example.test/v1", JudgeAPIKey: "sk-test-secret"}
	_, body := h.do(t, "GET", "/api/status", "")
	if strings.Contains(string(body), "sk-test-secret") {
		t.Fatalf("status leaks the key: %s", body)
	}
	var resp struct {
		Judge map[string]any `json:"judge"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	j := resp.Judge
	if j["calls_24h"] != float64(3) || j["key_set"] != true || j["url"] != "https://llm.example.test/v1" || j["last_error"] != "HTTP 429" {
		t.Errorf("judge = %v", j)
	}
}

func TestStatusWithoutJudgeRunner(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/api/status", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"judge"`) {
		t.Errorf("status %d: %s", resp.StatusCode, body)
	}
}

// enableHotukdeals turns the second site on and gives it one passed deal.
func enableHotukdeals(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	cur := h.svc.Current()
	next := cur.Settings
	next.Sources = maps.Clone(next.Sources)
	src := next.Sources["hotukdeals"]
	src.Enabled = true
	next.Sources["hotukdeals"] = src
	if _, err := h.svc.Save(ctx, cur.Version, next); err != nil {
		t.Fatal(err)
	}
	at := h.api.Now().Add(-time.Hour)
	d := source.Deal{ID: "hotukdeals:z", Source: "hotukdeals", URL: "https://www.hotukdeals.com/deals/x-z",
		Title: "Example Kettle", Merchant: "Example Shop", Price: "9", Category: "Home", Temperature: intp(200)}
	if _, err := h.st.UpsertDeal(ctx, d, source.FeedTrending, "example kettle", "shop|z", at); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SaveVerdict(ctx, store.Verdict{DealID: d.ID, Decision: "pass", Score: 200, DecidedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func TestListDealsSourceFilter(t *testing.T) {
	h := newHarness(t)
	enableHotukdeals(t, h)
	var got struct {
		Deals []struct {
			ID          string `json:"id"`
			Source      string `json:"source"`
			SourceLabel string `json:"source_label"`
		} `json:"deals"`
		Counts map[string]int `json:"counts"`
	}
	_, body := h.do(t, "GET", "/api/deals", "")
	if err := json.Unmarshal(body, &got); err != nil || len(got.Deals) != 2 {
		t.Fatalf("all sources: %v %s", err, body)
	}
	for _, d := range got.Deals {
		if d.Source == "" || d.SourceLabel != d.Source {
			t.Errorf("deal %+v lacks source/label", d)
		}
	}
	got.Deals = nil
	_, body = h.do(t, "GET", "/api/deals?source=hotukdeals", "")
	json.Unmarshal(body, &got)
	if len(got.Deals) != 1 || got.Deals[0].ID != "hotukdeals:z" || got.Counts["passed"] != 1 {
		t.Errorf("filtered = %+v counts %v", got.Deals, got.Counts)
	}
	resp, body := h.do(t, "GET", "/api/deals?source=dealabs", "")
	if resp.StatusCode != 400 || !strings.Contains(string(body), `"unknown source"`) {
		t.Errorf("disabled source = %d %s", resp.StatusCode, body)
	}
}

func TestStatusListsEnabledSources(t *testing.T) {
	h := newHarness(t)
	enableHotukdeals(t, h)
	_, body := h.do(t, "GET", "/api/status", "")
	var got struct {
		Feeds   []any `json:"feeds"`
		Sources []struct{ Name, Label string }
	}
	if err := json.Unmarshal(body, &got); err != nil || len(got.Feeds) != 1 || len(got.Sources) != 2 ||
		got.Sources[0].Name != "mydealz" || got.Sources[1].Name != "hotukdeals" || got.Sources[1].Label != "hotukdeals" {
		t.Errorf("status = %v %s", err, body)
	}
}

func TestCategoriesOnlyOfEnabledSources(t *testing.T) {
	h := newHarness(t)
	enableHotukdeals(t, h)
	_, body := h.do(t, "GET", "/api/categories", "")
	if !strings.Contains(string(body), `"Home"`) {
		t.Fatalf("both enabled: %s", body)
	}
	cur := h.svc.Current()
	next := cur.Settings
	next.Sources = maps.Clone(next.Sources)
	src := next.Sources["hotukdeals"]
	src.Enabled = false
	next.Sources["hotukdeals"] = src
	if _, err := h.svc.Save(context.Background(), cur.Version, next); err != nil {
		t.Fatal(err)
	}
	_, body = h.do(t, "GET", "/api/categories", "")
	if strings.Contains(string(body), `"Home"`) || !strings.Contains(string(body), `"Elektronik"`) {
		t.Errorf("only mydealz: %s", body)
	}
}

func TestListDealsPriceHistory(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	now := h.api.Now()
	seed := func(id, title, price string, firstSeen time.Time) {
		d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://example.test/" + id,
			Title: title, Merchant: "Example Air", Price: price, Temperature: intp(200)}
		if _, err := h.st.UpsertDeal(ctx, d, source.FeedTrending, strings.ToLower(title), "air|"+id, firstSeen); err != nil {
			t.Fatal(err)
		}
		if err := h.st.SaveVerdict(ctx, store.Verdict{DealID: d.ID, Decision: "pass", Score: 200, DecidedAt: firstSeen}); err != nil {
			t.Fatal(err)
		}
	}
	seed("p1", "Galaxy S24 128GB", "599€", now.Add(-time.Hour))
	seed("p2", "Samsung Galaxy S24 128GB", "549€", now.AddDate(0, 0, -40)) // too old to be listed, still history
	if _, err := h.st.ReindexProducts(ctx, product.Index); err != nil {
		t.Fatal(err)
	}

	_, body := h.do(t, "GET", "/api/deals", "")
	var got struct {
		Deals []struct {
			ID           string           `json:"id"`
			PriceHistory *product.History `json:"price_history"`
		} `json:"deals"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	byID := map[string]*product.History{}
	for _, d := range got.Deals {
		byID[d.ID] = d.PriceHistory
	}
	p1, ok := byID["mydealz:p1"]
	if !ok || p1 == nil {
		t.Fatalf("p1 has no price history: %s", body)
	}
	if p1.Status != "above" || p1.DiffCents != 5000 || p1.Count != 1 || p1.Lowest.ID != "mydealz:p2" || p1.Lowest.Price != "549€" {
		t.Errorf("p1 history = %+v", p1)
	}
	if byID["mydealz:a"] != nil {
		t.Errorf("deal without model code has history: %+v", byID["mydealz:a"])
	}
	if strings.Count(string(body), `"price_history"`) != 1 {
		t.Errorf("price_history should be omitted when empty: %s", body)
	}
}

func TestRepeatClickIsAccepted(t *testing.T) {
	h := newHarness(t)
	var first, second store.Action
	for i, dst := range []*store.Action{&first, &second} {
		resp, body := h.do(t, "POST", "/api/actions", `{"kind":"hide_deal","deal_id":"mydealz:a"}`)
		if resp.StatusCode != 201 {
			t.Fatalf("click %d status = %d: %s", i+1, resp.StatusCode, body)
		}
		json.Unmarshal(body, dst)
	}
	if first.ID == 0 || second.ID != first.ID {
		t.Errorf("ids = %d, %d, want the same action", first.ID, second.ID)
	}
	if actions, _ := h.st.Actions(context.Background()); len(actions) != 1 {
		t.Errorf("actions = %+v, want one", actions)
	}
	if resp, _ := h.do(t, "POST", "/api/actions/undo", `{"kind":"hide_deal","deal_id":"mydealz:a"}`); resp.StatusCode != http.StatusNoContent {
		t.Errorf("undo = %d", resp.StatusCode)
	}
	if actions, _ := h.st.Actions(context.Background()); len(actions) != 0 {
		t.Errorf("actions after undo = %+v", actions)
	}
}
