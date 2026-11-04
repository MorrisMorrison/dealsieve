package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/digest"
	"github.com/MorrisMorrison/dealsieve/internal/judge"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
)

func TestGetAndPutSettings(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/api/settings", "")
	if resp.StatusCode != 200 {
		t.Fatalf("GET %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Version  int64          `json:"version"`
		Settings map[string]any `json:"settings"`
	}
	json.Unmarshal(body, &got)

	const tpl = `{"version":%d,"settings":{"watchlist":["oled"],"rules":{"min_temperature":80,"category_overrides":{},"muted_categories":[],"muted_merchants":[],"block_keywords":[],"recurrence":{"window_days":%d,"max_seen":2}},"channels":{},"notify":[],"suggestions":{"min_count":3,"window_days":30}}}`
	put := fmt.Sprintf(tpl, got.Version, 90)
	resp, body = h.do(t, "PUT", "/api/settings", put)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"rechecked":7`) || !strings.Contains(string(body), `"changed":true`) {
		t.Fatalf("PUT %d: %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, "PUT", "/api/settings", put); resp.StatusCode != http.StatusConflict || errorCode(body) != "stale" {
		t.Errorf("stale PUT = %d %s, want 409 with code stale", resp.StatusCode, body)
	}

	bad := fmt.Sprintf(tpl, h.svc.Current().Version, 0)
	resp, body = h.do(t, "PUT", "/api/settings", bad)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), `"rules.recurrence.window_days"`) {
		t.Errorf("invalid PUT %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "PUT", "/api/settings", `{"version":1,"settings":{"nope":1}}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown key = %d, want 400", resp.StatusCode)
	}
}

func TestSettingsResponsesNeverContainTokens(t *testing.T) {
	h := newHarness(t)
	h.api.Env.TelegramToken, h.api.Env.NtfyToken = "TGTOKEN-12345", "NTFYTOKEN-67890"
	_, get := h.do(t, "GET", "/api/settings", "")
	put := fmt.Sprintf(`{"version":%d,"settings":{"watchlist":[],"rules":{"min_temperature":100,"category_overrides":{},"muted_categories":[],"muted_merchants":[],"block_keywords":[],"recurrence":{"window_days":90,"max_seen":2}},"channels":{"tg":{"type":"telegram","chat_id":"1"}},"notify":[],"suggestions":{"min_count":3,"window_days":30}}}`, h.svc.Current().Version)
	resp, putBody := h.do(t, "PUT", "/api/settings", put)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT %d: %s", resp.StatusCode, putBody)
	}
	for name, body := range map[string][]byte{"GET": get, "PUT": putBody} {
		for _, tok := range []string{"TGTOKEN-12345", "NTFYTOKEN-67890"} {
			if strings.Contains(string(body), tok) {
				t.Errorf("%s /api/settings contains %s: %s", name, tok, body)
			}
		}
	}
}

func TestCrossOriginSettingsPutIsRejected(t *testing.T) {
	h := newHarness(t)
	req, err := http.NewRequest("PUT", h.srv.URL+"/api/settings", strings.NewReader(`{"version":1,"settings":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site PUT = %d, want 403", resp.StatusCode)
	}
}

func TestUndoAndHistory(t *testing.T) {
	h := newHarness(t)
	undo := func() string { return fmt.Sprintf(`{"version":%d}`, h.svc.Current().Version) }
	if resp, body := h.do(t, "POST", "/api/settings/undo", undo()); resp.StatusCode != http.StatusConflict || errorCode(body) != "nothing_to_undo" {
		t.Errorf("undo with nothing to undo = %d %s, want 409 with code nothing_to_undo", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "POST", "/api/settings/undo", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("undo without a version = %d, want 400", resp.StatusCode)
	}
	h.do(t, "POST", "/api/actions", `{"kind":"mute_category","deal_id":"mydealz:a"}`)
	resp, body := h.do(t, "GET", "/api/settings/history", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `Added muted category \"Elektronik\" (from a deal)`) {
		t.Errorf("history %d: %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, "POST", "/api/settings/undo", `{"version":1}`); resp.StatusCode != http.StatusConflict || errorCode(body) != "stale" {
		t.Errorf("undo with a stale version = %d %s, want 409 with code stale", resp.StatusCode, body)
	}
	if resp, body := h.do(t, "POST", "/api/settings/undo", undo()); resp.StatusCode != 200 || !strings.Contains(string(body), `"muted_categories":[]`) {
		t.Errorf("undo %d: %s", resp.StatusCode, body)
	}
}

func TestTestChannel(t *testing.T) {
	h := newHarness(t)
	var got map[string]any
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer ntfy.Close()
	resp, body := h.do(t, "POST", "/api/settings/test", fmt.Sprintf(`{"channel":{"type":"ntfy","url":%q,"topic":"deals"}}`, ntfy.URL))
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) || got["title"] != "dealsieve test notification" {
		t.Errorf("test send %d: %s (ntfy got %v)", resp.StatusCode, body, got)
	}
	resp, body = h.do(t, "POST", "/api/settings/test", `{"channel":{"type":"telegram","chat_id":"1"}}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "DEALSIEVE_TELEGRAM_TOKEN") {
		t.Errorf("telegram without token %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "POST", "/api/settings/test", `{"channel":{"type":"ntfy"}}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("invalid channel = %d", resp.StatusCode)
	}
}

func TestTestChannelTokenOnlyToBoundHost(t *testing.T) {
	h := newHarness(t)
	var authBound, authOther string
	bound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { authBound = r.Header.Get("Authorization") }))
	defer bound.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { authOther = r.Header.Get("Authorization") }))
	defer other.Close()
	bu, _ := url.Parse(bound.URL)
	h.api.Env.NtfyToken, h.api.Env.NtfyTokenHost = "secret", bu.Host

	for _, u := range []string{bound.URL, other.URL} {
		resp, body := h.do(t, "POST", "/api/settings/test", fmt.Sprintf(`{"channel":{"type":"ntfy","url":%q,"topic":"deals"}}`, u))
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
			t.Fatalf("test send %d: %s", resp.StatusCode, body)
		}
	}
	if authBound != "Bearer secret" || authOther != "" {
		t.Errorf("bound got %q, other got %q", authBound, authOther)
	}
}

// The page's test send must not hand the target's answer to the browser.
func TestTestChannelHidesTargetResponseBody(t *testing.T) {
	h := newHarness(t)
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "SECRET-BODY", http.StatusInternalServerError)
	}))
	defer ntfy.Close()
	resp, body := h.do(t, "POST", "/api/settings/test", fmt.Sprintf(`{"channel":{"type":"ntfy","url":%q,"topic":"deals"}}`, ntfy.URL))
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":false`) || !strings.Contains(string(body), "ntfy: status 500") {
		t.Errorf("test send %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "SECRET-BODY") {
		t.Errorf("response leaks the target's body: %s", body)
	}
}

func TestSuggestionsAndCategories(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "GET", "/api/suggestions", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"suggestions":[]`) {
		t.Errorf("suggestions %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "POST", "/api/suggestions/apply", `{"kind":"nope","value":"x"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad kind = %d", resp.StatusCode)
	}
	resp, body = h.do(t, "POST", "/api/suggestions/apply", `{"kind":"mute_merchant","value":"Example Shop"}`)
	if resp.StatusCode != 200 || !strings.Contains(fmt.Sprint(h.svc.Current().Settings.Rules.MutedMerchants), "Example Shop") {
		t.Errorf("apply %d: %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, "POST", "/api/suggestions/dismiss", `{"kind":"mute_category","value":"Reisen"}`); resp.StatusCode != 200 {
		t.Errorf("dismiss = %d", resp.StatusCode)
	}
	resp, body = h.do(t, "GET", "/api/categories", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"Elektronik"`) {
		t.Errorf("categories %d: %s", resp.StatusCode, body)
	}
}

// A change that was saved and applied stays a success when only the re-check
// of recent deals fails.
func TestRecheckFailureAfterSaveIsAWarning(t *testing.T) {
	h := newHarness(t)
	h.rc.err = errors.New("db is busy")

	resp, body := h.do(t, "POST", "/api/actions", `{"kind":"mute_category","deal_id":"mydealz:a"}`)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"warning"`) {
		t.Errorf("mute %d: %s", resp.StatusCode, body)
	}
	resp, body = h.do(t, "POST", "/api/actions", `{"kind":"mute_merchant","deal_id":"mydealz:a"}`)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(body), `re-checking recent deals failed`) {
		t.Errorf("mute merchant %d: %s", resp.StatusCode, body)
	}
	resp, body = h.do(t, "POST", "/api/settings/undo", fmt.Sprintf(`{"version":%d}`, h.svc.Current().Version))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"changed":true`) ||
		!strings.Contains(string(body), "they'll be re-checked on the next poll") {
		t.Errorf("undo %d: %s", resp.StatusCode, body)
	}
	resp, body = h.do(t, "POST", "/api/suggestions/apply", `{"kind":"mute_category","value":"Reisen"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"warning"`) {
		t.Errorf("apply %d: %s", resp.StatusCode, body)
	}
	if got := h.svc.Current().Settings.Rules.MutedCategories; len(got) != 2 {
		t.Errorf("muted categories = %v, want the saved ones", got)
	}
}

type fakeDigest struct{ calls int }

func (f *fakeDigest) SendTest(context.Context) []digest.TestResult {
	f.calls++
	return []digest.TestResult{{Channel: "phone", OK: true}, {Channel: "tg", Error: "telegram sendMessage: status 400"}}
}

func TestTestDigest(t *testing.T) {
	h := newHarness(t)
	fd := &fakeDigest{}
	h.api.Digest = fd
	resp, body := h.do(t, "POST", "/api/settings/digest/test", "")
	if resp.StatusCode != http.StatusUnprocessableEntity || fd.calls != 0 {
		t.Fatalf("no digest channels: %d %s", resp.StatusCode, body)
	}

	s := h.svc.Current().Settings
	s.Channels = map[string]settings.Channel{"phone": {Type: "ntfy", URL: "https://ntfy.example.com", Topic: "deals"}}
	s.Digest.Channels = []string{"phone"}
	if _, err := h.svc.Save(context.Background(), h.svc.Current().Version, s); err != nil {
		t.Fatal(err)
	}
	resp, body = h.do(t, "POST", "/api/settings/digest/test", "")
	if resp.StatusCode != 200 || fd.calls != 1 ||
		!strings.Contains(string(body), `{"channel":"phone","ok":true}`) || !strings.Contains(string(body), `"error":"telegram sendMessage: status 400"`) {
		t.Errorf("test digest %d: %s", resp.StatusCode, body)
	}
}

func TestTestJudge(t *testing.T) {
	h := newHarness(t)
	fake := &fakeJudgeRunner{result: judge.TestResult{OK: true, Rating: judge.Bad, Reason: "Plain re-post."}}
	h.api.Judge = fake
	resp, body := h.do(t, "POST", "/api/settings/judge/test", `{"judge":{"model":"","daily_limit":200}}`)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "judge.model") {
		t.Errorf("invalid judge = %d: %s", resp.StatusCode, body)
	}
	resp, body = h.do(t, "POST", "/api/settings/judge/test", `{"judge":{"enabled":true,"model":" example/model ","daily_limit":200,"instructions":"be strict"}}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) || !strings.Contains(string(body), `"rating":"bad"`) {
		t.Errorf("valid judge = %d: %s", resp.StatusCode, body)
	}
	if fake.got.Model != "example/model" || fake.got.Instructions != "be strict" {
		t.Errorf("runner saw %+v", fake.got)
	}
}

func TestTestJudgeWithoutRunner(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, "POST", "/api/settings/judge/test", `{"judge":{"model":"example/model","daily_limit":200}}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d: %s", resp.StatusCode, body)
	}
}

func TestJudgeKeyNeverInResponses(t *testing.T) {
	h := newHarness(t)
	h.api.Env = config.Env{JudgeURL: "https://llm.example.test/v1", JudgeAPIKey: "sk-test-secret"}
	h.api.Judge = &fakeJudgeRunner{result: judge.TestResult{OK: false, Error: "HTTP 401"}}
	_, get := h.do(t, "GET", "/api/settings", "")
	var parsed struct {
		JudgeEnv map[string]any `json:"judge_env"`
	}
	if err := json.Unmarshal(get, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.JudgeEnv["key_set"] != true || parsed.JudgeEnv["url"] != "https://llm.example.test/v1" {
		t.Errorf("judge_env = %v", parsed.JudgeEnv)
	}
	_, status := h.do(t, "GET", "/api/status", "")
	_, test := h.do(t, "POST", "/api/settings/judge/test", `{"judge":{"model":"example/model","daily_limit":200}}`)
	for name, b := range map[string][]byte{"settings": get, "status": status, "test": test} {
		if strings.Contains(string(b), "sk-test-secret") {
			t.Errorf("%s leaks the key: %s", name, b)
		}
	}
}

func TestSettingsBodyListsSites(t *testing.T) {
	h := newHarness(t)
	_, body := h.do(t, "GET", "/api/settings", "")
	var got struct {
		Sites []struct{ Name, Label, Domain string }
	}
	if err := json.Unmarshal(body, &got); err != nil || len(got.Sites) != 6 {
		t.Fatalf("sites: %v %s", err, body)
	}
	if last := got.Sites[5]; last.Name != "pepperpl" || last.Label != "pepper.pl" || last.Domain != "pepper.pl" {
		t.Errorf("last site = %+v", last)
	}
	if got.Sites[1].Domain != "hotukdeals.com" {
		t.Errorf("second site = %+v", got.Sites[1])
	}
}

func errorCode(body []byte) string {
	var e struct {
		Code string `json:"code"`
	}
	json.Unmarshal(body, &e)
	return e.Code
}
