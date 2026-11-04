package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if resp, _ := h.do(t, "PUT", "/api/settings", put); resp.StatusCode != http.StatusConflict {
		t.Errorf("stale PUT = %d, want 409", resp.StatusCode)
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

func TestUndoAndHistory(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.do(t, "POST", "/api/settings/undo", ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("undo with nothing to undo = %d", resp.StatusCode)
	}
	h.do(t, "POST", "/api/actions", `{"kind":"mute_category","deal_id":"mydealz:a"}`)
	resp, body := h.do(t, "GET", "/api/settings/history", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `Added muted category \"Elektronik\" (from a deal)`) {
		t.Errorf("history %d: %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, "POST", "/api/settings/undo", ""); resp.StatusCode != 200 || !strings.Contains(string(body), `"muted_categories":[]`) {
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
	resp, body = h.do(t, "POST", "/api/settings/undo", "")
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
