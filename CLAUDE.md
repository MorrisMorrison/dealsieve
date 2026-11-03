# CLAUDE.md

Guidance for Claude Code (and anyone else) working in this repository. Keep it short and correct: when behaviour, config, commands or layout change, update this file and `README.md` in the same PR.

## What this is
dealsieve is a small self-hosted filter for deal feeds. One Go binary polls a source's feeds, stores every deal in SQLite, runs each one through an ordered rule engine that records *why* it passed or was blocked, serves the result on an embedded web page, and sends optional ntfy/Telegram notifications. Its first and only source is mydealz: the `trending` feed fills the page, the `new` feed (every new deal) is only matched against the watchlist. The design and its amendments are in `docs/superpowers/specs/2026-11-03-dealsieve-design.md`; the v1 plan in `docs/superpowers/plans/`.

## Layout
| Path | Responsibility |
|---|---|
| `cmd/dealsieve/` | `main.go`: wiring only (config, store, pipeline, poller, prune loop, HTTP server, graceful shutdown) and the `healthcheck` subcommand used by the image's `HEALTHCHECK` |
| `internal/config/` | YAML config + `DEALSIEVE_*` env vars, defaults and validation (all problems reported at once) |
| `internal/source/` | `Deal`, `Feed` and the `Fetcher` interface; `source/mydealz/` is the RSS adapter (fixtures in `testdata/`) |
| `internal/fingerprint/` | `CleanTitle` + `Key`: merchant plus sorted distinct title words without numbers, so re-posts of the same offer share a key |
| `internal/rules/` | The rule engine: `Evaluate(Deal, Facts) Result` — pure, no I/O |
| `internal/pipeline/` | Ingest (store → evaluate → save verdict → notify), `RecomputeRecent` after a user action, `Prune`; serialised by one mutex |
| `internal/poller/` | One loop per feed with its own interval, backoff on failures (capped at 30 min), per-feed status for the page |
| `internal/notify/` | `Notifier` interface, ntfy and Telegram implementations |
| `internal/store/` | SQLite (modernc, pure Go): deals, temperature snapshots, verdicts, actions, notification bookkeeping; numbered migrations in `migrations/` tracked by `PRAGMA user_version` |
| `internal/api/` | `net/http` mux: JSON API (`/api/deals`, `/api/actions`, `/api/rules`, `/api/status`), `/health`, the embedded page |
| `internal/judge/` | Placeholder interface for a later LLM opinion; v1 ships only `Noop` |
| `internal/version/` | `Version`, stamped with `-ldflags -X` from the `VERSION` build arg |
| `web/` | The page: `index.html`, `static/css`, `static/js` (vanilla ES modules, no build step), embedded by `embed.go` |
| `docs-site/` | The docs website (Astro + Starlight): hand-written Markdown pages in `src/content/docs/`, published to GitHub Pages |
| `test/e2e/` | End-to-end test: builds the real binary and runs it against fake feed and ntfy servers |

## Commands
```bash
gofmt -l .                                  # must print nothing
go vet ./...
go test -race ./...                         # unit tests (CI runs -race)
go test -tags e2e ./test/e2e/               # builds the binary, starts it with fake servers, stops it
node --test web/static/js/*.test.js         # frontend unit tests (Node 24, no packages)
CGO_ENABLED=0 go build ./cmd/dealsieve      # must stay CGO-free (distroless static image)
```
Docs site (in `docs-site/`): `npm ci`, `npm run dev` (preview), `npm run build` (also fails on a broken internal link). Edit the Markdown in `docs-site/src/content/docs/`; to add a page, create the file and list it in the sidebar in `astro.config.mjs`. Links inside pages need the `/dealsieve/` base.

Running it needs a `config.yaml` (start from `config.example.yaml`) and writes `./data/dealsieve.db`. Both are git-ignored.

## How tests isolate the outside world
- **Nothing in the tests talks to mydealz, ntfy or Telegram.** Sources and notifiers take a base URL and an `*http.Client`; tests point them at `httptest.Server`s. The e2e test serves `internal/source/mydealz/testdata/*.xml` as `/rss/trending` and `/rss/alles` and sets `DEALSIEVE_MYDEALZ_URL` to that server.
- **Store tests use a real SQLite file** in `t.TempDir()`, never a shared database.
- **Time is injected** (`Now func() time.Time` on the pipeline and API server); tests pass a fixed clock instead of sleeping.
- **Fixtures are made up** (`Example Shop`, `Example Air`, `static.example.test`). Never copy real feed content into the repo. If the feed format changes, change the fixture by hand to match.

## Conventions
- **Go:** standard library first (`net/http` with method patterns, `log/slog`, `database/sql`); the only direct dependencies are `modernc.org/sqlite` and `gopkg.in/yaml.v3`. No web framework. Ask before adding a dependency. Wrap errors with `%w`. Log a constant message plus key/value attrs. Comments are sparse and explain *why*.
- **Rule order is the contract.** `rules.blockReason` checks, in order: hidden → muted category → muted merchant → block keyword → recurring → junk fingerprint → (watchlist hits stop here) → too cold → new-only. The first block reason wins and is listed first in `Reasons`. Watchlist hits get `+1000` score and are exempt from the recurrence and temperature rules on purpose. A passing deal also records pass reasons (temperature, recurrence) after the watchlist reason. A new rule needs a place in that order, a human-readable reason string, and a table test in `rules_test.go`.
- **Keywords match the raw lowercased title**, not the cleaned one (cleaning drops numbers like `5080`).
- **Fingerprints:** changing `CleanTitle`/`Key` changes every stored fingerprint's meaning (recurrence counts and junk actions are keyed by it). Treat it as a migration: say so in the PR and decide what happens to existing rows.
- **Verdicts are recomputed** for the last 7 days after every action (and at startup, so config changes apply). Recompute never notifies.
- **Notifications:** at most one successful send per (deal, channel), up to 3 attempts, failed ones retried on later polls. Keep that bookkeeping in `notifications_sent`.
- **Schema changes** go in a new `internal/store/migrations/NNN_*.sql`; never edit an applied migration.
- **Config:** every setting has a default in `config.defaults()` and is validated in `validate()`. Secrets come only from env vars (`DEALSIEVE_TELEGRAM_TOKEN`, `DEALSIEVE_NTFY_TOKEN`), never from the YAML. New settings go in `config.example.yaml` and the README table.
- **API:** errors are `{"error": "..."}`. Writes are protected by `http.CrossOriginProtection`; every response gets `nosniff` and `no-referrer`. The page has no login by design — it's meant for a private network or behind a reverse proxy's auth.
- **Front-end:** plain HTML, CSS and ES modules; no framework, no npm packages, no build step. Build the DOM with `textContent` and attributes, never `innerHTML` with data (deal titles are untrusted). Pure helpers (`format.js`, `sparkline.js`) get a `*.test.js` next to them.
- **Being a good feed citizen:** keep default poll intervals reasonable, send the identifying `User-Agent` (`dealsieve/<version> (+repo URL)`), link every deal back to the original post.

## Public repo
This repository is public. Keep it free of personal infrastructure (domains, hostnames, private registries, tokens, chat IDs) in code, fixtures, commits, issues and PRs; examples use `example.com`/`example.test`. Describe the project as a filter for deal feeds with mydealz as its first source — not as an alternative to, or replacement for, any site. Grep the diff before pushing.

## Workflow
- Every change starts from a GitHub issue; one branch and one PR per issue, `Closes #N` in the PR body.
- Branch names: `feat/…`, `fix/…`, `chore/…`, `docs/…`.
- PR description: what changed, why, and what was verified (and what wasn't — e.g. "not run against the live feeds").
- CI (`ci.yml`: gofmt, vet, `-race` tests, e2e, frontend tests; Docker build on PRs; `docs.yml` builds the docs site on PRs and deploys it from `main`) must be green before merge.
- A push to `main` publishes `ghcr.io/morrismorrison/dealsieve:latest` and `:main-<sha>`; a `vX.Y.Z` tag publishes `:X.Y.Z` (never moves `:latest`). The same tag also runs `release.yml`, which creates the GitHub release (pre-release for `v0.*`) with generated notes. Tag only a green `main` HEAD, after the release-process PR is merged.
</content>
</invoke>
