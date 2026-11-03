---
name: add-source
description: Add a new deal source or notification channel to dealsieve, or fix the mydealz adapter after a feed change. Use when asked to support another deal site or feed, parse a new field, handle a changed RSS format, or add a notifier besides ntfy and Telegram.
---

# Add a source or a notifier

Both are small adapters behind an interface, configured with a base URL and an `*http.Client` so tests can point them at an `httptest.Server`.

## A source (`internal/source/<name>/`)
1. Implement `source.Fetcher`: `Fetch(ctx, feed) ([]source.Deal, error)` for `FeedTrending` and `FeedNew`. IDs are `"<source>:<id at the source>"`; `Title` has no temperature prefix; `Temperature` is nil when the feed has none.
2. Send the identifying `User-Agent` the mydealz client gets from `main.go`. Return an error (don't return zero deals) when the response isn't the expected format, e.g. an HTML error page — the poller backs off on errors.
3. Fixtures in `testdata/`: **made-up deals only** (`Example Shop`, `static.example.test`), never copied feed content. Test parsing from the fixture and `Fetch` against an `httptest.Server` (path, `User-Agent`, status errors), like `mydealz_test.go`.
4. Config: a base-URL env var `DEALSIEVE_<NAME>_URL` in `config.envFrom` (for tests), README table. Wiring in `cmd/dealsieve/main.go`. The rules, fingerprints and store are source-agnostic; the deal ID prefix keeps sources apart.
5. Extend `test/e2e` if the source changes what the page shows.

## A notifier (`internal/notify/`)
1. Implement `notify.Notifier` (`Send(ctx, Message) error`); non-2xx is an error, so the pipeline retries (max 3 attempts per deal and channel).
2. Add the channel `type` to `config.Channel` validation and `notify.FromConfig`. Secrets (tokens) come from `DEALSIEVE_*` env vars only, never the YAML.
3. Test against an `httptest.Server` in `notify_test.go`: request shape, auth header, error on failure.
4. Document it in `config.example.yaml` and the README.

## Verify
```bash
gofmt -l . && go vet ./... && go test -race ./... && go test -tags e2e ./test/e2e/
```
Say in the PR that it was verified against fixtures, not the live site/service, unless you checked that too.
