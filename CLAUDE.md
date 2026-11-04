# CLAUDE.md

Guidance for Claude Code (and anyone else) working in this repository. Keep it short and correct: when behaviour, config, commands or layout change, update this file and `README.md` in the same PR.

## What this is
dealsieve is a small self-hosted filter for deal feeds. One Go binary polls a source's feeds, stores every deal in SQLite, runs each one through an ordered rule engine that records *why* it passed or was blocked, serves the result on an embedded web page, and sends optional ntfy/Telegram notifications. Its first source is mydealz; the other Pepper-network sites (hotukdeals, dealabs, preisjaeger, chollometro, pepper.pl) can be turned on in Settings. Per site, the `trending` feed fills the page, the `new` feed (every new deal) is only matched against the watchlist. The design and its amendments are in `docs/superpowers/specs/2026-11-03-dealsieve-design.md`; the v1 plan in `docs/superpowers/plans/`.

## Layout
| Path | Responsibility |
|---|---|
| `cmd/dealsieve/` | `main.go`: wiring only (config, store, pipeline, poller, prune loop, HTTP server, graceful shutdown) and the `healthcheck` subcommand used by the image's `HEALTHCHECK` |
| `internal/config/` | `DEALSIEVE_*` env vars only (paths, poll intervals, secrets, `DEALSIEVE_PUBLIC_URL`); no YAML |
| `internal/settings/` | The user settings document (JSON): `Defaults`, `Validate` (all problems keyed by field path), `Summarize` (change summaries), conversions for the rules engine and notifiers |
| `internal/settingsvc/` | Saves, undoes and applies settings (versions, conflict check, swap into the pipeline, recompute) and serves suggestions |
| `internal/suggest/` | Pure: mute suggestions from Hide/Junk clicks, settings and dismissals |
| `internal/source/` | `Deal`, `Feed` and the `Fetcher` interface; `source/pepper/` is the RSS adapter (fixtures in `testdata/`) |
| `internal/fingerprint/` | `CleanTitle` + `Key`: merchant plus sorted distinct title words without plain numbers or sizes (`16gb`, `2x`); model codes like `s24` stay. Re-posts of the same offer share a key |
| `internal/product/` | Pure: `ParsePrice` (cents + currency from `1.449€`, `£12.99`), `Of`/`Same` (same product = identical model codes (spaced `iPhone 15` as fallback), the same variant/accessory/condition words, no size conflict per unit, ≥ 60% shared other words), `Compare` (price history for one deal) |
| `internal/rules/` | The rule engine: `Evaluate(Deal, Facts) Result` — pure, no I/O |
| `internal/pipeline/` | Ingest (store → evaluate → save verdict → notify), `RecomputeRecent` after a user action, `Prune`; serialised by one mutex |
| `internal/poller/` | One loop per source and feed with its own interval, backoff on failures (capped at 30 min), per-feed status for the page |
| `internal/digest/` | Daily digest: `Due`, `Compose`, and the `Runner` that sends due digests every minute outside the pipeline lock; per-channel window ends in `digest_state`, deals already listed per channel in `digest_sent` |
| `internal/notify/` | `Notifier` interface, ntfy and Telegram implementations |
| `internal/store/` | SQLite (modernc, pure Go): deals, temperature snapshots, verdicts, actions, notification bookkeeping, price-history index (`deal_products`); numbered migrations in `migrations/` tracked by `PRAGMA user_version` |
| `internal/api/` | `net/http` mux: JSON API (`/api/deals`, `/api/actions` (incl. `GET`, the "Your clicks" list), `/api/settings*` (incl. `POST /api/settings/judge/test`), `/api/suggestions*`, `/api/categories`, `/api/status`), `/health`, the embedded pages |
| `internal/judge/` | LLM judge: OpenAI-compatible client, prompt/reply parsing, the background `Runner` |
| `internal/version/` | `Version`, stamped with `-ldflags -X` from the `VERSION` build arg |
| `web/` | The pages: `index.html` (inbox), `settings.html` (Settings), `static/css`, `static/js` (vanilla ES modules, no build step), embedded by `embed.go` |
| `docs-site/` | The docs website (Astro + Starlight): hand-written Markdown pages in `src/content/docs/`, published to GitHub Pages |
| `screenshots/` | README screenshot tool (Node + Playwright, its own `package.json`): made-up demo `settings.json` (applied through `PUT /api/settings`) + RSS `feeds/`, `capture.mjs` runs the built binary against a fake feed server and writes `docs/screenshots/` (git-ignored). The `screenshots.yml` workflow publishes them to the `screenshots` branch |
| `test/e2e/` | End-to-end test: builds the real binary and runs it against fake feed and ntfy servers |

## Commands
```bash
gofmt -l .                                  # must print nothing
go vet ./...
go test -race ./...                         # unit tests (CI runs -race)
go test -tags e2e ./test/e2e/               # builds the binary, starts it with fake servers, stops it
node --test web/static/js/*.test.js         # frontend unit tests (Node 24, no packages)
CGO_ENABLED=0 go build ./cmd/dealsieve      # must stay CGO-free (distroless static image)
# README screenshots (on Windows build -o dealsieve.exe): needs the built binary, ~2 min (waits for two 1m polls); first time also: npx playwright install chromium
CGO_ENABLED=0 go build -o dealsieve ./cmd/dealsieve && (cd screenshots && npm ci && npm run capture)
```
Docs site (in `docs-site/`): `npm ci`, `npm run dev` (preview), `npm run build` (also fails on a broken internal link). Edit the Markdown in `docs-site/src/content/docs/`; to add a page, create the file and list it in the sidebar in `astro.config.mjs`. Links inside pages need the `/dealsieve/` base.

Running it needs nothing but a data dir (it writes `./data/dealsieve.db`, git-ignored); configure it on the Settings page.

## How tests isolate the outside world
- **Nothing in the tests talks to any Pepper site, ntfy or Telegram.** Sources and notifiers take a base URL and an `*http.Client`; tests point them at `httptest.Server`s. The e2e test serves the mydealz fixtures (`trending.xml`, `new.xml`) as `/rss/trending` and `/rss/alles` and the hotukdeals ones as `/rss/trending` and `/rss/new` from a second server, and sets `DEALSIEVE_MYDEALZ_URL` and `DEALSIEVE_HOTUKDEALS_URL` to them.
- **Store tests use a real SQLite file** in `t.TempDir()`, never a shared database.
- **Time is injected** (`Now func() time.Time` on the pipeline and API server); tests pass a fixed clock instead of sleeping.
- **Fixtures are made up** (`Example Shop`, `Example Air`, `static.example.test`). Never copy real feed content into the repo. If the feed format changes, change the fixture by hand to match.

## Conventions
- **Go:** standard library first (`net/http` with method patterns, `log/slog`, `database/sql`); the only direct dependency is `modernc.org/sqlite`. No web framework. Ask before adding a dependency. Wrap errors with `%w`. Log a constant message plus key/value attrs. Comments are sparse and explain *why*.
- **Rule order is the contract.** `rules.blockReason` checks, in order: hidden → muted category → muted merchant → block keyword → recurring → junk fingerprint → (watchlist hits stop here) → too cold → new-only. The first block reason wins and is listed first in `Reasons`. Watchlist hits get `+1000` score and are exempt from the recurrence and temperature rules on purpose. A passing deal also records pass reasons (temperature, recurrence) after the watchlist reason. A new rule needs a place in that order, a human-readable reason string, and a table test in `rules_test.go`.
- **Keywords match the raw lowercased title**, not the cleaned one (cleaning drops numbers like `5080`).
- **Fingerprints:** changing `CleanTitle`/`Key` changes every stored fingerprint's meaning (recurrence counts and junk actions are keyed by it). Treat it as a migration: say so in the PR and decide what happens to existing rows. `Store.Rekey` runs at startup and re-keys deals and junk actions to the current rules.
- **Recurrence counts only the same source:** `CountRecurrence` ignores deals from other sources (the same offer on two sites is not a repeat); Junk, Hide, the watchlist, mutes, keywords and category overrides apply across sources. A new Pepper site is a row in `pepper.sites`, a name in `settings.SourceNames` (plus a label if it differs), fixtures in `internal/source/pepper/testdata/` and a docs line.
- **Price history is information only.** It never changes a verdict, score, notification or the digest. `deal_products` is rebuilt from all deals at startup (`ReindexProducts`), so a parser or matching change needs no migration.
- **Recurrence only looks back:** `CountRecurrence` counts deals with the same fingerprint first seen *before* the deal (same-poll ties by id), inside the window ending at its own first sighting, so a recompute never turns an earlier pass into "recurring".
- **Verdicts are recomputed** for the last 7 days after every action and after every settings change (and at startup). Recompute never notifies.
- **Notifications:** at most one successful send per (deal, channel), up to 3 attempts, failed ones retried on later polls. Keep that bookkeeping in `notifications_sent`.
- **Schema changes** go in a new `internal/store/migrations/NNN_*.sql`; never edit an applied migration.
- **Settings:** user settings live in the `settings` document (validated in `settings.Validate`, summarised in `settings.Summarize`); every setting needs a default in `settings.Defaults`, a validation rule, a summary line, a form control on the Settings page and a docs-site entry. Env vars are for infrastructure and secrets only (`DEALSIEVE_TELEGRAM_TOKEN`, `DEALSIEVE_NTFY_TOKEN` never enter the settings, the database or an API response); a new env var goes in `config.FromEnv`, the README table and the docs-site configuration page.
- **The ntfy token is bound to a host:** with `DEALSIEVE_NTFY_TOKEN_HOST` set, `notify.ForChannel` attaches the token only to a channel whose URL host matches (`settings.NtfyBinding.Sends`); unset, it goes to every ntfy channel (warned at startup and via `settings.Warnings`). Any new ntfy send path must build its notifier through `ForChannel`.
- **Mutes are settings, Hide/Junk are actions.** `POST /api/actions` routes `mute_category`/`mute_merchant` to the settings lists (a new version); settings saves are versioned, conflict-checked with `version`, and applied without a restart.
- **The judge only annotates:** opinions never change a verdict or notify; the model is never called under the pipeline lock. Its URL and key are env vars (`DEALSIEVE_JUDGE_URL`, `DEALSIEVE_JUDGE_API_KEY`) so the key can't be redirected from the login-less page.
- **API:** errors are `{"error": "..."}`. Writes are protected by `http.CrossOriginProtection`; every response gets `nosniff` and `no-referrer`. The page has no login by design — it's meant for a private network or behind a reverse proxy's auth.
- **Front-end:** plain HTML, CSS and ES modules; no framework, no npm packages in `web/`, no build step (npm packages exist only in the dev tooling: `screenshots/` and `docs-site/`). Build the DOM with `textContent` and attributes, never `innerHTML` with data (deal titles are untrusted). Pure helpers (`format.js`, `sparkline.js`) get a `*.test.js` next to them.
- **Being a good feed citizen:** keep default poll intervals reasonable, send the identifying `User-Agent` (`dealsieve/<version> (+repo URL)`), link every deal back to the original post.

## Public repo
This repository is public. Keep it free of personal infrastructure (domains, hostnames, private registries, tokens, chat IDs) in code, fixtures, commits, issues and PRs; examples use `example.com`/`example.test`. Describe the project as a filter for deal feeds with mydealz as its first source — not as an alternative to, or replacement for, any site. Grep the diff before pushing.

## Workflow
- Every change starts from a GitHub issue; one branch and one PR per issue, `Closes #N` in the PR body.
- Branch names: `feat/…`, `fix/…`, `chore/…`, `docs/…`.
- PR description: what changed, why, and what was verified (and what wasn't — e.g. "not run against the live feeds").
- CI (`ci.yml`: gofmt, vet, `-race` tests, e2e, frontend tests; Docker build on PRs; `docs.yml` builds the docs site on PRs and deploys it from `main`) must be green before merge. Unit-test coverage is uploaded to Codecov via OIDC (no token); `codecov.yml` keeps the statuses informational, never blocking.
- A push to `main` publishes `ghcr.io/morrismorrison/dealsieve:latest` and `:main-<sha>`; a `vX.Y.Z` tag publishes `:X.Y.Z` (never moves `:latest`). The same tag also runs `release.yml`, which creates the GitHub release (pre-release for `v0.*`) with generated notes. Tag only a green `main` HEAD, after the release-process PR is merged.
</content>
</invoke>
