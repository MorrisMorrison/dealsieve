# dealsieve — design (v1)

> A self-hosted filter for deal feeds. It reads a deal community's feeds, throws away the noise
> (recurring pseudo-deals, unwanted categories, low-quality posts), and shows what's left on a small
> web page, with optional push notifications. v1 has one source: mydealz.

Status: approved design, 2026-11-03; amended the same day after checking the live feeds (see
"Amendments" at the end). Not affiliated with any deal site; meant for personal use.

## Problem

Big deal communities are noisy. The worst kind of noise is the **recurring pseudo-deal**: the same
offer pattern posted every few weeks ("flights from €19.99") that looks cheap on price alone but
isn't once hidden costs are added, and often isn't below the normal price either. On top of that:
categories you never care about, low-quality posts (samples, sweepstakes, cashback), and simply too
much volume.

## Goals

- Show only deals that pass your rules, with the **reason** for every decision visible.
- Catch recurring pseudo-deals automatically, from the tool's own history.
- Learn from one-click feedback on the page ("junk", "mute category", ...).
- Instant ping for watchlist keywords, as soon as a matching deal is posted.
- One Go binary, one SQLite file, one YAML file. Easy to self-host.

## Non-goals (v1)

- Detecting fake "before" prices. The feeds don't carry an old price. That needs the tool's own
  price history (a later phase with shop sources) or an LLM judge.
- LLM judgment. There is an interface for it, but v1 ships only a no-op.
- Multi-user accounts or in-app login. Run it behind your reverse proxy or auth layer.
- Editing rules in the web page. Stable rules live in YAML; the page only records click actions.
- Hot config reload. Config changes are picked up on restart.
- Fuzzy title matching. v1 uses exact fingerprint matching.

## Stack

- **Go**, standard library `net/http` (method + path patterns); no web framework.
- **SQLite** via `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`), migrations embedded.
- **Frontend:** static HTML + CSS + vanilla ES modules, embedded with `go:embed`. No framework,
  no build step, no npm dependencies. The page talks to the backend only through the JSON API.
- `log/slog` for logging.

## Architecture

```
mydealz RSS (trending, alles)
        │  poller (interval per feed)
        ▼
  source adapter ──► store (SQLite): deals, temperature snapshots, verdicts, actions
        │
        ▼
  fingerprint ──► recurrence count
        │
        ▼
  rules pipeline ──► verdict: pass / block (+ boosts, notify flags, reasons)
        │                         │
        ▼                         ▼
  JSON API ◄── web page      notifiers (ntfy, Telegram)
```

### Packages

| Package | Job |
|---|---|
| `cmd/dealsieve` | Wiring only: config, store, poller, server. |
| `internal/config` | Loads and validates YAML; secrets come from env. |
| `internal/source` | `Source` interface returning `[]Deal`. v1: the `mydealz` adapter. |
| `internal/store` | SQLite access and embedded migrations. |
| `internal/fingerprint` | Title normalization and the fingerprint key. |
| `internal/rules` | Ordered rule pipeline; each rule returns a decision and a reason. |
| `internal/judge` | LLM judge interface. v1: `Noop`. |
| `internal/notify` | `Notifier` interface; `ntfy` and `telegram` implementations. |
| `internal/pipeline` | The ingest → verdict → notify cycle, verdict recomputation, pruning. |
| `internal/poller` | Schedules feed polls, jitter and backoff; keeps per-feed status. |
| `internal/version` | Version string, set at build time. |
| `internal/api` | JSON endpoints and `/health`. |
| `web/` | Embedded `index.html`, CSS, ES modules. |

## Source: mydealz feeds

Feeds (RSS 2.0, 30 items each): `/rss/trending` (internal feed name `trending`) and `/rss/alles`
(internal feed name `new`). `/rss/hot` and `/rss/new` only redirect to these two, so they aren't
polled separately.

Per item the adapter reads: `title`, `link`, `category`, `media:content` (image URL),
`pepper:merchant` (attributes `name`, `price`; either or both may be missing). Namespaces:
`media` = `http://search.yahoo.com/mrss/`, `pepper` = `http://www.pepper.com/rss`.

- **Temperature** is a title prefix in `trending` (`"142° - …"`, may be negative) and is parsed
  out. Items in `alles` carry **no temperature**; theirs stays unknown until the deal shows up in
  `trending`.
- **Deal ID** = `mydealz:<thread ID>`, the thread ID being the number at the end of the deal URL. It
  merges duplicates across feeds; the prefix keeps IDs unique once other sources exist.
- The link must be `http(s)` with a host; otherwise the item is skipped.
- **`pubDate` isn't used.** `first_seen` (when dealsieve first saw the deal) drives all time-based
  logic.
- **Feed roles:**
  - `trending` → the deal enters the main pipeline and can show on the page.
  - `new` → used **only for watchlist matching** (instant ping). A deal seen only in `new` doesn't
    show on the page unless it matches the watchlist, and it shows in the page's "Watchlist hits" tab.

## Data model

| Table | Columns (main) |
|---|---|
| `deals` | `id` (`mydealz:<thread ID>`), `source`, `url`, `title`, `title_clean`, `merchant`, `price`, `category`, `image_url`, `first_seen`, `last_seen`, `in_trending`, `in_new`, `fingerprint`, `temperature` (latest known, nullable) |
| `temperature_snapshots` | `deal_id`, `at`, `temperature` (one row per poll that saw the deal) |
| `verdicts` | `deal_id`, `decision` (`pass`/`block`), `score`, `watchlist_hit`, `reasons` (JSON array), `decided_at` |
| `actions` | `id`, `kind` (`mute_category`, `mute_merchant`, `hide_deal`, `junk`), `value`, `created_at` |
| `notifications_sent` | `deal_id`, `channel`, `sent_at`, `attempts` |

- Deals are kept for **180 days** (the recurrence window is 90 days by default), then removed
  with their snapshots and verdicts.
- Verdicts are **recomputed** for a deal on every poll that sees it (temperature changes), and for
  all deals seen in the last 7 days after any action is added or undone and at startup (so YAML
  changes apply). Because a deal is re-checked as it heats up, a `min_temperature` notify rule fires
  once the deal crosses the threshold.

## Fingerprinting

`title_clean` = lowercase → remove the temperature prefix → remove prices and other numbers →
remove punctuation and a small stopword list (German and English) → collapse whitespace.

`fingerprint` = `merchant` (lowercase, or `-` if missing) + `|` + the remaining words, sorted and
de-duplicated. Exact matching only in v1.

**Recurrence count** = the number of *distinct* deals with the same fingerprint whose `first_seen` is
inside the recurrence window, excluding the current deal.

## Rules pipeline

Keyword matching (watchlist and `block_keywords`) is a case-insensitive substring match on the
**full title**, not `title_clean`: cleaning removes numbers, and watchlist entries like `rtx 5080`
need them.

Before the rules run, the deal's **watchlist match** is computed. Rules 5, 7, 8 and 9 use it.

Rules run in this order. The first `block` decides; boosts add up; the reasons from every rule that
fired are kept.

1. **Hidden** — the deal has a `hide_deal` action → block.
2. **Muted category** — YAML `muted_categories` or a `mute_category` action → block.
3. **Muted merchant** — YAML `muted_merchants` or a `mute_merchant` action → block.
4. **Block keyword** — the title contains a YAML `block_keywords` entry → block.
5. **Recurring** — recurrence count ≥ `recurrence.max_seen` → block. Doesn't apply to watchlist hits.
   Reason: `"recurring: 4th time in 90 days (last 2026-10-12)"`.
6. **Like your junk** — the fingerprint equals the fingerprint of a deal marked `junk` → block.
7. **Temperature** — current temperature < `min_temperature` (or the category override) → block.
   Doesn't apply to watchlist hits or to deals whose temperature is unknown.
8. **Watchlist** — the deal has a watchlist match → `watchlist_hit = true`, boost.
   Watchlist hits still go through rules 1–4 and 6.
9. **New-only** — the deal has only been seen in the `new` feed and isn't a watchlist hit → block
   (not shown in the Blocked tab either; it was never a page candidate).
10. Otherwise → pass.

`score` = current temperature + boosts (watchlist boost: +1000, so hits sort on top). Page sort is
by score, then `first_seen`.

## Notifications

Notify rules in YAML map a condition to channels. Conditions in v1: `watchlist` and
`{ min_temperature: N }`. A deal is sent at most once per channel (`notifications_sent`).

- Only deals with decision `pass` are sent.
- A failed send is retried on later poll cycles, up to 3 attempts. The row is marked sent only
  after success.
- **ntfy:** POST to `url/topic` with title, a message (price, temperature, merchant, reason), a
  `Click` header (deal URL), an `Attach` header (image URL), and priority. Optional token from env.
- **Telegram:** `sendPhoto` with the image and a caption (title, price, temperature, reason, link);
  falls back to `sendMessage` without an image. The bot token comes from env.

## Config

```yaml
poll:
  trending: 5m
  new: 2m                 # watchlist matching only
rules:
  min_temperature: 100
  category_overrides: { "Elektronik": 60 }
  muted_categories: [Fashion & Accessoires, Verträge]
  muted_merchants: []
  block_keywords: [gratisprobe, gewinnspiel, cashback]
  recurrence: { window: 90d, max_seen: 2 }
watchlist: [oled, rtx 5080, lego technic]
notify:
  - when: watchlist
    channels: [ntfy-high, telegram]
  - when: { min_temperature: 500 }
    channels: [ntfy-default]
channels:
  ntfy-high:    { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: high }
  ntfy-default: { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: default }
  telegram:     { type: telegram, chat_id: "123456789" }
```

Env vars: `DEALSIEVE_CONFIG` (default `./config.yaml`), `DEALSIEVE_DATA_DIR` (default `./data`),
`DEALSIEVE_LISTEN` (default `:8080`), `DEALSIEVE_TELEGRAM_TOKEN`, `DEALSIEVE_NTFY_TOKEN` (optional),
`DEALSIEVE_LOG_FORMAT` (`text`/`json`).

Validation at startup: unknown keys, unknown channel types, notify rules naming channels that don't
exist, intervals below 1 minute, and a missing token for a configured Telegram channel are all
startup errors.

## JSON API

| Method + path | Purpose |
|---|---|
| `GET /api/deals?view=passed\|watchlist\|blocked` | Deals seen in the last 3 days, with verdict, reasons, current temperature and the temperature history (for the sparkline). At most 200, sorted by score. Also returns `counts` (`passed`, `watchlist`, `blocked`) for the same window, for the tab badges. |
| `POST /api/actions` | Body `{kind, deal_id}`; adds an action and recomputes verdicts. |
| `DELETE /api/actions/{id}` | Undoes an action and recomputes verdicts. |
| `GET /api/rules` | Active YAML rules (read-only) and the list of actions. |
| `GET /api/status` | Per feed: last successful poll, last error. |
| `GET /health` | Liveness for monitors. |

## Web page

One page, mobile-friendly, light/dark theme (follows the system, with a toggle). Laid out as an
inbox with a detail panel (issue #3).

- Header: a live feed-status line from `/api/status` ("Feeds healthy · updated 2 min ago", or a
  red stale warning when a feed hasn't polled successfully for 3× its interval), a **Rules** button
  and the theme toggle.
- Tabs: **Passed** · **Watchlist** · **Blocked**, each with its count (`counts` from `/api/deals`).
- List: thumbnail, one-line title, price, shop, temperature coloured by heat, age; blocked deals
  show their block reason, watchlist hits a watchlist tag. The first deal is selected.
- Detail panel: full title, price, a large heat curve (inline SVG from the snapshots), "Why it's
  here" / "Why it's blocked" from the verdict's reasons, category, the feeds the deal was seen in,
  an **Open deal** button and Hide / Junk / Mute shop / Mute category.
- Phones: the list is full width; a deal opens as a full-screen sheet with a back button.
- Keyboard: ↑/↓ (or j/k) moves the selection, Enter opens the deal, Esc closes the sheet; the tabs
  use `tab`/`tabpanel` roles with arrow-key switching.
- Filters: category, text search. Both filter the loaded list in the browser.
- **Rules view** (behind the Rules button): the list of actions, each with **undo**, and the
  effective rules (read-only).

## Polling and errors

- An honest `User-Agent` (`dealsieve/<version> (+<repo URL>)`), redirects followed, ±10% jitter,
  a 1-minute minimum interval. (The feeds send no `ETag` / `Last-Modified`, so there are no
  conditional requests.)
- A `200` response that isn't valid RSS (e.g. a bot-check HTML page) counts as a failed poll.
- `429` / `5xx` / network errors → exponential backoff per feed (capped at 30 min), recorded in
  `/api/status`.
- An item that fails to parse is logged and skipped; the rest of the feed is processed.
- Images are linked from the source, never downloaded or re-hosted.

## Testing

- Parser tests on recorded feeds in `testdata/` (hot, trending, new; incl. items without a merchant
  or price).
- Table tests per rule and for fingerprinting, using real recurring-title variants.
- Store tests on a temporary SQLite file.
- Notifier tests against `httptest` fakes for ntfy and Telegram.
- An end-to-end test (`go test -tags e2e`): builds the binary, serves fixture feeds from a fake
  server, and checks verdicts and notifications through the API.
- CI (GitHub Actions, hosted runners): `go vet`, `go test -race`, e2e, Docker build on PRs, and an
  image published to GHCR on `main`.

## Deployment

A small multi-stage Docker image (static binary, non-root user, `/data` volume, `HEALTHCHECK` on
`/health`). Configure through a mounted `config.yaml` and env vars. The page has no login; put it
behind a reverse proxy or auth layer if it isn't only on a private network.

## Amendments (2026-11-03, after checking the live feeds)

- `/rss/hot` redirects to `/rss/trending` and `/rss/new` to `/rss/alles`: the feeds are `trending`
  and `alles`; the `hot` feed, its poll setting and the `in_hot` column are gone.
- `alles` items carry no temperature; unknown temperatures skip the temperature rule.
- No `ETag` / `Last-Modified` on the feeds: conditional requests dropped.
- Keyword and watchlist matching use the full title (cleaning removes numbers like `5080`).
- Deal IDs are prefixed with the source (`mydealz:<thread ID>`); `pubDate` is not stored.
- The deal list carries each deal's temperature history, replacing a separate
  `/api/deals/{id}/temperatures` endpoint (one request per page load instead of one per card).
- Category and search filtering happen in the browser; the page lists deals seen in the last 3 days.
- Env `DEALSIEVE_MYDEALZ_URL` (default `https://www.mydealz.de`) overrides the feed host, for tests.
- Watchlist hits skip the recurring rule (5) as well as the temperature rule (7). Fingerprints drop
  model numbers, so different products from one shop can share a key and look recurring, and the
  watchlist is explicit user intent. Hidden, muted, block keyword and junk still apply to them.
- The Rules view (a tab until issue #3) shows the effective rules (after defaults) as read-only JSON; "open" is the deal
  title link.
- The action body is `{kind, deal_id}`; the server derives the value from the deal (hide: the deal ID,
  junk: its fingerprint, mute: its category or merchant), so clients can't inject arbitrary values.
- The API rejects cross-origin state-changing requests (stdlib cross-origin protection) and sends
  `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer` on every response.
- Pass verdicts now record why they passed (temperature against the minimum, recurrence), after the
  watchlist reason; block reasons are unchanged.
- Settings moved from `config.yaml` into an in-app Settings page (issue #21); see
  `docs/superpowers/specs/2026-11-04-settings-page-design.md`. `config.yaml` and `DEALSIEVE_CONFIG` are no longer read.

## Later

- ~~**LLM judge** behind the `judge` interface, only for deals that already passed the rules.~~
  Done (issue #39); see `docs/superpowers/specs/2026-11-04-llm-judge-design.md`.
- **Shop sources and price history** (phase B): own adapters, price history per product, real
  fake-discount detection.
- Fuzzy fingerprint matching, hot config reload, more notifiers.
