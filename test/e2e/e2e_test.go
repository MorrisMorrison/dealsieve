//go:build e2e

// The e2e test builds the real binary and runs it against fake feed and ntfy
// servers. Run it with: go test -tags e2e ./test/e2e/
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

type deal struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Reasons []string `json:"reasons"`
	Opinion *struct {
		Rating string `json:"rating"`
		Reason string `json:"reason"`
	} `json:"opinion"`
}

func TestEndToEnd(t *testing.T) {
	bin := buildBinary(t)

	feeds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss/trending":
			http.ServeFile(w, r, fixture("trending.xml"))
		case "/rss/alles":
			http.ServeFile(w, r, fixture("new.xml"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer feeds.Close()

	var mu sync.Mutex
	var pushes []map[string]any
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		pushes = append(pushes, p)
		mu.Unlock()
	}))
	defer ntfy.Close()

	// Fake chat-completions upstream for the judge.
	var judgeModels, judgeAuth []string
	judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		judgeModels = append(judgeModels, req.Model)
		judgeAuth = append(judgeAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"rating\":\"bad\",\"reason\":\"Fees add about €70.\"}"}}]}`))
	}))
	defer judge.Close()

	dir := t.TempDir()
	// Settings must exist before the first poll: re-checks never notify, so
	// deals that arrive earlier couldn't trigger the push.
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dataDir, "dealsieve.db"))
	if err != nil {
		t.Fatal(err)
	}
	seed := settings.Defaults()
	seed.Watchlist = []string{"rtx 5080"}
	seed.Rules.BlockKeywords = []string{"gratisprobe"}
	seed.Channels = map[string]settings.Channel{"push": {Type: "ntfy", URL: ntfy.URL, Topic: "deals", Priority: "high"}}
	seed.Notify = []settings.NotifyRule{{When: settings.When{Watchlist: true}, Channels: []string{"push"}}}
	doc, _ := json.Marshal(seed)
	if _, err := st.SaveSettings(context.Background(), doc, []string{"e2e seed"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	st.Close()

	listen := "127.0.0.1:" + freePort(t)
	env := append(os.Environ(),
		"DEALSIEVE_POLL_TRENDING=1m",
		"DEALSIEVE_POLL_NEW=1m",
		"DEALSIEVE_DATA_DIR="+dataDir,
		"DEALSIEVE_LISTEN="+listen,
		"DEALSIEVE_MYDEALZ_URL="+feeds.URL,
		"DEALSIEVE_JUDGE_URL="+judge.URL+"/v1",
		"DEALSIEVE_JUDGE_API_KEY=",
	)
	var logs bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	base := "http://" + listen

	waitFor(t, "two passed deals", func() bool { return len(getDeals(base, "passed")) == 2 })
	if got := ids(getDeals(base, "passed")); got != "mydealz:1000001,mydealz:1000002" {
		t.Errorf("passed = %s", got)
	}

	waitFor(t, "the watchlist hit", func() bool { return len(getDeals(base, "watchlist")) == 1 })
	if got := getDeals(base, "watchlist")[0]; got.ID != "mydealz:1000010" {
		t.Errorf("watchlist = %+v", got)
	}

	blocked := getDeals(base, "blocked")
	if ids(blocked) != "mydealz:1000003" || !strings.HasPrefix(blocked[0].Reasons[0], "too cold") {
		t.Errorf("blocked = %+v (the new-only keyword deal must not show)", blocked)
	}

	waitFor(t, "the ntfy push", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(pushes) == 1
	})
	mu.Lock()
	if pushes[0]["title"] != "Example RTX 5080 Grafikkarte 16GB" || pushes[0]["priority"] != float64(4) {
		t.Errorf("push = %v", pushes[0])
	}
	mu.Unlock()

	resp, err := http.Post(base+"/api/actions", "application/json", strings.NewReader(`{"kind":"hide_deal","deal_id":"mydealz:1000001"}`))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("hide: %v %v", resp, err)
	}
	resp.Body.Close()
	if got := ids(getDeals(base, "passed")); got != "mydealz:1000002" {
		t.Errorf("passed after hide = %s", got)
	}
	if got := getDeals(base, "blocked"); len(got) != 2 || got[0].ID != "mydealz:1000001" || got[0].Reasons[0] != "hidden by you" {
		t.Errorf("blocked after hide = %+v", got)
	}

	// Settings changes apply live: muting the shop blocks its watchlist deal.
	resp, err = http.Get(base + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var cur struct {
		Version  int64          `json:"version"`
		Settings map[string]any `json:"settings"`
	}
	json.NewDecoder(resp.Body).Decode(&cur)
	resp.Body.Close()
	cur.Settings["rules"].(map[string]any)["muted_merchants"] = []string{"Example Shop"}
	cur.Settings["judge"] = map[string]any{"enabled": true, "model": "example/model", "daily_limit": 50, "instructions": ""}
	put, _ := json.Marshal(map[string]any{"version": cur.Version, "settings": cur.Settings})
	req, _ := http.NewRequest(http.MethodPut, base+"/api/settings", bytes.NewReader(put))
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings: %v %v", resp, err)
	}
	resp.Body.Close()
	var muted *deal
	for _, d := range getDeals(base, "blocked") {
		if d.ID == "mydealz:1000010" {
			muted = &d
		}
	}
	if muted == nil || muted.Reasons[0] != "muted merchant: Example Shop" {
		t.Errorf("blocked after muting the shop = %+v", getDeals(base, "blocked"))
	}
	if got := getDeals(base, "watchlist"); len(got) != 0 {
		t.Errorf("watchlist after muting the shop = %+v", got)
	}

	// The same save enabled the judge, which annotates passed deals.
	waitForWithin(t, 30*time.Second, "the judge's opinion", func() bool {
		for _, d := range getDeals(base, "passed") {
			if d.Opinion != nil && d.Opinion.Rating == "bad" && d.Opinion.Reason == "Fees add about €70." {
				return true
			}
		}
		return false
	})
	mu.Lock()
	if len(judgeModels) == 0 || judgeModels[0] != "example/model" || judgeAuth[0] != "" {
		t.Errorf("judge saw models %v, auth %q", judgeModels, judgeAuth)
	}
	mu.Unlock()

	hc := exec.Command(bin, "healthcheck")
	hc.Env = env
	if out, err := hc.CombinedOutput(); err != nil {
		t.Errorf("healthcheck: %v %s", err, out)
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "dealsieve")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, "../../cmd/dealsieve").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func fixture(name string) string {
	return filepath.Join("..", "..", "internal", "source", "mydealz", "testdata", name)
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

func getDeals(base, view string) []deal {
	resp, err := http.Get(base + "/api/deals?view=" + view)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Deals []deal `json:"deals"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	return body.Deals
}

func ids(ds []deal) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return strings.Join(out, ",")
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitForWithin(t, 20*time.Second, what, cond)
}

func waitForWithin(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
