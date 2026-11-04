# Settings page — design

> Configure dealsieve in the app instead of `config.yaml`: watchlist, filters, recurrence, notification
> channels and rules, plus rule suggestions learned from the user's Hide/Junk clicks. Issue #21.

Status: approved design, 2026-11-04.

## Goals

- Every filtering and notification setting is edited on a **Settings page** and applies **without a
  restart** (verdicts of the last 7 days are re-checked after a save).
- **One place to configure:** the page. `config.yaml` and `DEALSIEVE_CONFIG` are removed.
- **History and undo:** every save is kept as a version; "Undo last change" restores the previous one.
- **Rule suggestions:** repeated Hide/Junk clicks on one shop or category produce an explained
  suggestion to mute it, with Apply and Dismiss.
- Secrets never live in the database or on the page.

## Non-goals

- Importing an existing `config.yaml` (no installs with custom configs exist; v0.0.1 is the only release).
- Keyword suggestions from junked titles (later).
- "Dismiss for N days" (dismissals are permanent).
- Editing polling intervals, sources, data dir, listen address or secrets on the page (they stay
  environment variables).
- Login. The page stays unauthenticated by design (private network or reverse-proxy auth); writes stay
  protected by `CrossOriginProtection`.

## Configuration split

| Where | What |
|---|---|
| Settings page (SQLite) | watchlist; muted shops; muted categories; block keywords; minimum temperature; per-category minimums; recurrence (window days, max seen); notification channels (ntfy: url, topic, priority; Telegram: chat ID); notify rules; suggestion thresholds |
| Environment | `DEALSIEVE_DATA_DIR`, `DEALSIEVE_LISTEN`, `DEALSIEVE_LOG_FORMAT`, `DEALSIEVE_MYDEALZ_URL`, **`DEALSIEVE_POLL_TRENDING`** (default `5m`), **`DEALSIEVE_POLL_NEW`** (default `2m`; both minimum `1m`), secrets `DEALSIEVE_TELEGRAM_TOKEN`, `DEALSIEVE_NTFY_TOKEN` |

`DEALSIEVE_CONFIG` and `config.yaml` are no longer read. `config.example.yaml` is deleted.

## The settings document

One JSON document, validated as a whole:

```json
{
  "watchlist": ["oled", "rtx 5080"],
  "rules": {
    "min_temperature": 100,
    "category_overrides": { "Elektronik": 60 },
    "muted_categories": ["Fashion & Accessoires"],
    "muted_merchants": ["Example Air"],
    "block_keywords": ["gratisprobe"],
    "recurrence": { "window_days": 90, "max_seen": 2 }
  },
  "channels": {
    "phone": { "type": "ntfy", "url": "https://ntfy.example.com", "topic": "deals", "priority": "high" },
    "tg":    { "type": "telegram", "chat_id": "123" }
  },
  "notify": [
    { "when": "watchlist", "channels": ["phone", "tg"] },
    { "when": { "min_temperature": 500 }, "channels": ["phone"] }
  ],
  "suggestions": { "min_count": 3, "window_days": 30 }
}
```

**Defaults** (a fresh install, and the document saved on the first start of an upgraded install):
empty watchlist, mutes and keywords, `min_temperature` 100, no overrides, recurrence 90 days / 2,
no channels, no notify rules, suggestions 3 within 30 days.

**Validation** (all problems reported together, keyed by field path):
- `rules.min_temperature`: integer (may be negative); overrides: non-empty category names, integer values.
- `rules.recurrence.window_days` ≥ 1 and ≤ 180 (deals are kept 180 days, so a longer window would
  silently under-count); `max_seen` ≥ 1.
- List entries are trimmed, non-empty, and de-duplicated case-insensitively (de-duplication is a
  normalisation on save, not an error).
- Channel names: non-empty, `[a-z0-9-]`, unique. `type` is `ntfy` or `telegram`. ntfy needs an
  http(s) `url` with a host and a `topic`; `priority` is one of min/low/default/high/urgent or empty.
  Telegram needs `chat_id`.
- Notify rules: `when` is `"watchlist"` or `{"min_temperature": N}` with N > 0; at least one channel;
  every channel must exist.
- `suggestions.min_count` ≥ 2, `window_days` 1–180.
- Unknown JSON keys are rejected.

A Telegram channel while `DEALSIEVE_TELEGRAM_TOKEN` is unset is **not** a validation error: the API
reports it as a per-channel warning and sends fail (logged and retried as today).

## Storage

New migration `002_settings.sql`:

| Table | Columns |
|---|---|
| `settings_versions` | `id` (autoincrement), `document` (JSON), `summary` (JSON array of change strings), `created_at` |
| `dismissed_suggestions` | `kind` (`mute_merchant` / `mute_category`), `value` (lowercase), `created_at`; primary key `(kind, value)` |

The migration also deletes `actions` rows of kind `mute_category` / `mute_merchant`; muting now lives
in the settings lists. Hide and Junk remain actions.

- The **current settings** are the newest row. If there is none at startup, the defaults are saved as
  version 1 with summary `["Initial settings"]`.
- **Summaries** are computed on save by diffing the previous and new document, e.g.
  `Added watchlist "lego"`, `Removed muted shop "Example Air"`, `Minimum temperature 120° → 100°`,
  `Added channel "phone"`, `Changed notify rules`. A save with no differences is not stored.
- Versions are kept (no pruning needed at this size).

## Runtime

- On save (or undo, or applying a suggestion), the new document is turned into a rules engine and a
  set of notifiers and **swapped into the pipeline under its existing mutex**, then the last 7 days are
  recomputed (without notifying), exactly as after an action today.
- The Mute shop / Mute category buttons on a deal now **add the deal's merchant/category to the
  settings lists** (a new version with summary `Muted shop "X" (from a deal)`), instead of creating
  actions. The page's existing `POST /api/actions` calls keep working: the server routes those two
  kinds to the settings update.
- Polling intervals come from the env vars and need a restart, as before.

## Suggestions

Computed on read (`GET /api/suggestions`), never stored:

1. Take Hide and Junk actions created in the last `suggestions.window_days`, joined to their deal's
   merchant and category (actions whose deal was pruned are skipped).
2. Count distinct deals per lowercase merchant and per lowercase category; empty values are ignored.
3. Keep groups with count ≥ `suggestions.min_count` that are **not already muted** and **not
   dismissed**.
4. Each suggestion: `kind` (`mute_merchant` / `mute_category`), `value` (as last seen on a deal),
   `count`, `since` (earliest action in the group), and up to 3 example deal titles. Sorted by count,
   then most recent.

`POST /api/suggestions/apply {kind, value}` adds the value to the right list (new version, summary
`Muted shop "X" (suggested)`) and recomputes. `POST /api/suggestions/dismiss {kind, value}` records a
dismissal. Both return the updated suggestion list.

## API

| Method + path | Purpose |
|---|---|
| `GET /api/settings` | `{version, updated_at, settings, warnings}`; `warnings` maps channel names to messages (missing Telegram token). |
| `PUT /api/settings` | Body `{version, settings}`. `409 {"error"}` if `version` isn't the current one; `422 {"errors": {path: message}}` on validation errors; otherwise saves, swaps, recomputes and returns the new `GET` body plus `rechecked` (number of deals re-checked). |
| `POST /api/settings/undo` | Saves the previous version's document as a new version (summary `Undid: <previous summary>`); `409` if there is nothing to undo. |
| `GET /api/settings/history` | The last 20 versions: `{version, created_at, summary}`. |
| `POST /api/settings/test` | Body `{channel: {…}}`: sends a test message ("dealsieve test notification") through a channel **as given** (unsaved form values), after validating that one channel. Returns `{ok}` or `{error}` (e.g. `ntfy: status 404`). |
| `GET /api/suggestions` | `{suggestions: [...]}` |
| `POST /api/suggestions/apply`, `POST /api/suggestions/dismiss` | See above. |
| `GET /api/categories` | Distinct categories seen in stored deals, for the category pickers. |

`GET /api/rules` is removed. All write endpoints stay behind `CrossOriginProtection`.

## The page

A separate page `settings.html` (linked from the header's settings button; "← Back to deals" returns),
same look as the inbox. Layout as in the approved prototype:

- Left: section navigation (Suggestions with a count badge, Watchlist, Filters, Recurring deals,
  Notifications, History); on phones a horizontal scroller.
- **Suggestions:** each with its explanation and examples, **Apply** and **Dismiss**; the thresholds
  as a sentence.
- **Watchlist / muted shops / muted categories / block keywords:** chip inputs (Enter adds,
  × removes); categories offer the seen ones.
- **Minimum temperature** and **per-category minimums** (rows of category + degrees).
- **Recurring deals** as a sentence with two number inputs and the note that watchlist matches are exempt.
- **Notifications:** channel cards (ntfy: server, topic, priority; Telegram: chat ID) with **Send
  test** and the token warning; notify rules as sentences ("When a watchlist deal passes / a deal
  reaches N° → send to …").
- **History:** the last versions with their summaries and **Undo last change**.
- **Saving:** a sticky bar appears on the first change ("N unsaved changes", **Discard**, **Save**).
  Validation errors appear under their fields; a `409` offers to reload. Leaving with unsaved changes
  asks first. After a save a toast says "Saved · N recent deals re-checked".

The inbox shows a one-line banner "N suggestions to tidy your feed → Settings" when suggestions exist.

Same front-end rules as before: vanilla HTML/CSS/ES modules, no build step, DOM via `textContent` and
attributes only. Pure helpers (diffing the form against the loaded document, counting changes,
building the document from the form) live in their own module with `node --test` tests.

## Code layout

| Package | Change |
|---|---|
| `internal/settings` (new) | `Settings` type + JSON, `Defaults()`, `Validate() map[string]string`, `Summarize(prev, next) []string`, conversions to the rules engine's and notifiers' inputs. The rule/channel/notify types move here from `internal/config`. |
| `internal/config` | Shrinks to the environment: `Env` plus the poll intervals. No YAML, and the `yaml.v3` dependency is dropped. |
| `internal/store` | Migration 002; settings versions (save, latest, previous, history); dismissals; the Hide/Junk-with-deal query for suggestions; categories. |
| `internal/suggest` (new) | Pure: computes suggestions from actions-with-deals, settings and dismissals. |
| `internal/pipeline` | `ApplySettings` swaps the engine, notify rules and notifiers under the mutex. |
| `internal/api` | The endpoints above; `/api/rules` removed; mute actions routed to settings. |
| `internal/notify` | `FromSettings(channels, env, client)`; a test-send helper. |
| `cmd/dealsieve` | Load env, open store, load or create settings, build the pipeline from them. |
| `web/` | `settings.html`, `static/js/settings*.js`, CSS additions, the settings button + suggestion banner in the inbox. |
| `test/e2e`, `screenshots/` | Configure through `PUT /api/settings` instead of a YAML file. |

## Testing

- `settings`: validation table (every rule above), defaults, JSON round trip, `Summarize` cases.
- `store`: versions (save/latest/previous/history), the empty-start defaults, dismissals, the
  suggestion query, categories, migration 002 deleting mute actions.
- `suggest`: threshold, window, already muted, dismissed, case-insensitive grouping, examples, order.
- `pipeline`: applying settings changes verdicts and notification channels without a restart.
- `api`: each endpoint, including `409`, `422`, undo with nothing to undo, test-send against an
  `httptest` ntfy server, mute actions landing in settings.
- e2e: start with defaults, `PUT` a watchlist + ntfy channel, see the watchlist hit notified.
- Frontend: `node --test` for the pure settings helpers; a browser check (desktop, dark, phone).

## Docs and rollout

- README, `CLAUDE.md`, the docs site's configuration reference ("Settings page + environment
  variables") and the screenshot demo are updated; `config.example.yaml` is deleted.
- Released as **v0.0.2** with a "breaking: `config.yaml` is no longer read" note. The personal deploy
  drops its `config.yaml` mount and sets the poll env vars only if it changed them.
