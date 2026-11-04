---
name: add-source
description: Add a new deal source or notification channel to dealsieve, or fix the Pepper adapter after a feed change. Use when asked to support another deal site or feed, parse a new field, handle a changed RSS format, or add a notifier besides ntfy and Telegram.
---

# Add a source or a notifier

Both are small adapters behind an interface, configured with a base URL and an `*http.Client` so tests can point them at an `httptest.Server`.

## Another Pepper-network site
The RSS adapter in `internal/source/pepper/` serves mydealz and every other Pepper site. A new site needs only:
1. A row in the site table in `internal/source/pepper` (base URL, new-deals path; trending is `/rss/trending`).
2. A name in `settings.SourceNames` (and a label in the labels map if it differs from the name); config picks up `DEALSIEVE_<NAME>_URL` from the same list.
3. Fixtures `<site>-trending.xml` and `<site>-new.xml` in `internal/source/pepper/testdata/` (made-up deals only) and a parse test.
4. A docs line: README and the docs-site (Sources, configuration env table).

## A source from another network (`internal/source/<name>/`)
1. Implement `source.Fetcher`: `Fetch(ctx, feed) ([]source.Deal, error)` for `FeedTrending` and `FeedNew`. IDs are `"<source>:<id at the source>"`; `Title` has no temperature prefix; `Temperature` is nil when the feed has none.
2. Send the identifying `User-Agent` the Pepper clients get from `main.go`. Return an error (don't return zero deals) when the response isn't the expected format, e.g. an HTML error page — the poller backs off on errors.
3. Fixtures in `testdata/`: **made-up deals only** (`Example Shop`, `static.example.test`), never copied feed content. Test parsing from the fixture and `Fetch` against an `httptest.Server` (path, `User-Agent`, status errors), like `pepper_test.go`.
4. Config: a base-URL env var `DEALSIEVE_<NAME>_URL` in `config.FromEnv` (for tests), README table and the docs-site configuration page. Wiring in `cmd/dealsieve/main.go`. The rules, fingerprints and store are source-agnostic; the deal ID prefix keeps sources apart.
5. Extend `test/e2e` if the source changes what the page shows.

## A notifier (`internal/notify/`)
1. Implement `notify.Notifier` (`Send(ctx, Message) error`); non-2xx is an error, so the pipeline retries (max 3 attempts per deal and channel).
2. Add the channel `type` to channel validation in `internal/settings` (with its fields, a default, a `Summarize` line and a test) and to `notify.FromSettings` / `notify.ForChannel`. Secrets (tokens) come from `DEALSIEVE_*` env vars only, never the settings document, the database or an API response; warn on the channel (`settings.Warnings`) when its token is missing.
3. Test against an `httptest.Server` in `notify_test.go`: request shape, auth header, error on failure.
4. Add the channel to the Settings page (a card with its fields and the **Send test** button, which goes through `POST /api/settings/test`) and document it in the docs-site notifications and configuration pages and the README.

## Verify
```bash
gofmt -l . && go vet ./... && go test -race ./... && go test -tags e2e ./test/e2e/
```
Say in the PR that it was verified against fixtures, not the live site/service, unless you checked that too.
