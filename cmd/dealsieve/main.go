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
	"github.com/MorrisMorrison/dealsieve/internal/fingerprint"
	"github.com/MorrisMorrison/dealsieve/internal/pipeline"
	"github.com/MorrisMorrison/dealsieve/internal/poller"
	"github.com/MorrisMorrison/dealsieve/internal/settingsvc"
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
	env, err := config.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(env.LogFormat)
	if os.Getenv("DEALSIEVE_CONFIG") != "" {
		log.Warn("DEALSIEVE_CONFIG is no longer read; configure dealsieve on the Settings page")
	}

	if err := os.MkdirAll(env.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	st, err := store.Open(filepath.Join(env.DataDir, "dealsieve.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	// Notifiers get a shorter timeout: the Settings page's test send waits
	// for one, and a slow target shouldn't hold a poll up for long.
	feedClient := &http.Client{Timeout: 30 * time.Second}
	notifyClient := &http.Client{Timeout: 10 * time.Second}
	pipe := &pipeline.Pipeline{Store: st, Env: env, HTTP: notifyClient, Now: time.Now, Log: log}
	svc := &settingsvc.Service{Store: st, Apply: pipe, Now: time.Now}
	if err := svc.Load(ctx); err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Bring stored fingerprints (and junk clicks keyed by them) up to the
	// current fingerprint rules; a no-op unless those rules changed.
	if n, err := st.Rekey(ctx, fingerprint.Of, fingerprint.RekeyOld); err != nil {
		return fmt.Errorf("rekey fingerprints: %w", err)
	} else if n > 0 {
		log.Info("updated fingerprints", "deals", n)
	}

	// Apply the stored settings to recent deals before serving them.
	if _, err := pipe.RecomputeRecent(ctx); err != nil {
		return fmt.Errorf("recompute verdicts: %w", err)
	}

	src := &mydealz.Client{
		BaseURL:   env.MydealzURL,
		UserAgent: "dealsieve/" + version.Version + " (+" + repoURL + ")",
		HTTP:      feedClient,
		Log:       log,
	}
	pl := poller.New(src, pipe, map[source.Feed]time.Duration{
		source.FeedTrending: env.PollTrending,
		source.FeedNew:      env.PollNew,
	}, log)
	// Both must finish before the deferred st.Close(), or they hit a closed database.
	var wg sync.WaitGroup
	wg.Go(func() { pl.Run(ctx) })
	wg.Go(func() { pruneLoop(ctx, pipe, log) })

	srv := &http.Server{
		Addr: env.Listen,
		Handler: (&api.Server{
			Store: st, Recompute: pipe, Status: pl.Status,
			Settings: svc, Env: env, HTTP: notifyClient,
			Assets: web.Assets, Log: log, Now: time.Now,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("dealsieve started", "version", version.Version, "listen", env.Listen)

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
