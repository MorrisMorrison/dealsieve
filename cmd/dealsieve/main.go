// Command dealsieve polls deal feeds, filters them and serves the result.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/api"
	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/pipeline"
	"github.com/MorrisMorrison/dealsieve/internal/poller"
	"github.com/MorrisMorrison/dealsieve/internal/rules"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/source/mydealz"
	"github.com/MorrisMorrison/dealsieve/internal/store"
	"github.com/MorrisMorrison/dealsieve/internal/version"
	"github.com/MorrisMorrison/dealsieve/web"
)

const repoURL = "https://github.com/MorrisMorrison/dealsieve"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "dealsieve:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfgPath := os.Getenv("DEALSIEVE_CONFIG")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath, os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Env.LogFormat)

	if err := os.MkdirAll(cfg.Env.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	st, err := store.Open(filepath.Join(cfg.Env.DataDir, "dealsieve.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	httpc := &http.Client{Timeout: 30 * time.Second}
	pipe := &pipeline.Pipeline{
		Store:     st,
		Engine:    rules.New(cfg.Rules, cfg.Watchlist),
		Notify:    cfg.Notify,
		Notifiers: notify.FromConfig(cfg.Channels, cfg.Env, httpc),
		Now:       time.Now,
		Log:       log,
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Apply config changes to recent deals before serving them.
	if err := pipe.RecomputeRecent(ctx); err != nil {
		return fmt.Errorf("recompute verdicts: %w", err)
	}

	src := &mydealz.Client{
		BaseURL:   cfg.Env.MydealzURL,
		UserAgent: "dealsieve/" + version.Version + " (+" + repoURL + ")",
		HTTP:      httpc,
		Log:       log,
	}
	pl := poller.New(src, pipe, map[source.Feed]time.Duration{
		source.FeedTrending: cfg.Poll.Trending.D(),
		source.FeedNew:      cfg.Poll.New.D(),
	}, log)
	// Both must finish before the deferred st.Close(), or they hit a closed database.
	var wg sync.WaitGroup
	wg.Go(func() { pl.Run(ctx) })
	wg.Go(func() { pruneLoop(ctx, pipe, log) })

	srv := &http.Server{
		Addr: cfg.Env.Listen,
		Handler: (&api.Server{
			Store: st, Recompute: pipe, Status: pl.Status, Config: cfg,
			Assets: web.Assets, Log: log, Now: time.Now,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("dealsieve started", "version", version.Version, "listen", cfg.Env.Listen)

	var srvErr error
	select {
	case srvErr = <-errc:
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopShutdown()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr = err
		}
	}
	cancel()
	wg.Wait()
	return srvErr
}

func pruneLoop(ctx context.Context, pipe *pipeline.Pipeline, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := pipe.Prune(ctx); err != nil {
			log.Error("prune failed", "err", err)
		} else if n > 0 {
			log.Info("pruned old deals", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func newLogger(format string) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// healthcheck is for the Docker HEALTHCHECK; the image has no curl.
func healthcheck() int {
	addr := os.Getenv("DEALSIEVE_LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		addr = net.JoinHostPort(host, port)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
