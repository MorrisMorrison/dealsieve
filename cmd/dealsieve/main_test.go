package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/judge"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

func TestRunShutsDownCleanly(t *testing.T) {
	fixtures := filepath.Join("..", "..", "internal", "source", "mydealz", "testdata")
	feeds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss/trending":
			http.ServeFile(w, r, filepath.Join(fixtures, "trending.xml"))
		case "/rss/alles":
			http.ServeFile(w, r, filepath.Join(fixtures, "new.xml"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer feeds.Close()

	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := l.Addr().String()
	l.Close()
	dataDir := filepath.Join(dir, "data")
	t.Setenv("DEALSIEVE_POLL_TRENDING", "1m")
	t.Setenv("DEALSIEVE_POLL_NEW", "1m")
	t.Setenv("DEALSIEVE_DATA_DIR", dataDir)
	t.Setenv("DEALSIEVE_LISTEN", listen)
	t.Setenv("DEALSIEVE_MYDEALZ_URL", feeds.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get("http://" + listen + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("server never became healthy")
		}
		time.Sleep(100 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}

	st, err := store.Open(filepath.Join(dataDir, "dealsieve.db"))
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer st.Close()
	if _, err := st.ListDeals(context.Background(), "passed", nil, time.Time{}, 10); err != nil {
		t.Fatalf("database unusable after shutdown: %v", err)
	}
}

func TestNewServerTimeouts(t *testing.T) {
	srv := newServer(":0", http.NotFoundHandler())
	if srv.Addr != ":0" || srv.Handler == nil {
		t.Fatalf("addr/handler not set: %q %v", srv.Addr, srv.Handler)
	}
	for name, d := range map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	} {
		if d <= 0 {
			t.Errorf("%s not set", name)
		}
	}
	if srv.ReadTimeout < srv.ReadHeaderTimeout {
		t.Errorf("ReadTimeout %v below ReadHeaderTimeout %v", srv.ReadTimeout, srv.ReadHeaderTimeout)
	}
	// The slowest handler is the judge test (its own call timeout); a test
	// send is bounded by the notifier client timeout. Neither response may be cut off.
	longest := max(judge.CallTimeout, notifyTimeout, feedTimeout)
	if srv.WriteTimeout <= longest {
		t.Errorf("WriteTimeout %v must exceed the longest outbound timeout %v", srv.WriteTimeout, longest)
	}
}
