# Daily digest — design

Status: implemented (issue #26).

## Goal

One message a day per chosen channel with what passed since the previous digest: the watchlist hits as a recap, then the top deals. It is an **add-on**: instant pushes (notify rules) stay exactly as they are. Someone who finds temperature pushes too noisy can remove those rules and rely on the digest.

## Behaviour

- **Schedule:** daily at a set local time (`time`, default `18:00`) in a set time zone (`timezone`, default `Europe/Berlin`). A digest is due when the local time is at or past today's send time and the channel has had no successful digest at or after today's send time. If dealsieve was down at send time, the digest goes out when it starts again the same day; a day missed entirely is folded into the next digest (its window just covers more).
- **Window:** since the channel's last successful digest (`last_cutoff`); the moment the digest is composed is the new `cutoff`. The first digest for a channel looks back 24 hours.
- **Selection:** deals whose current verdict is `pass`, that were seen in a feed since the previous digest (`last_seen > last_cutoff`), and that this channel hasn't had in a digest yet (`digest_sent`). The window is on `last_seen`, not `first_seen`, because a deal usually passes hours after it is first seen: dealsieve sees almost every deal while it is still cold in the every-new-deal feed, and it passes once it trends. A deal missed by a timing edge (its row and its verdict are written separately) is picked up next time, since trending deals are re-polled every few minutes and their `last_seen` keeps moving.
  - **Watchlist hits** (`watchlist_hit = 1`): all of them, newest first, at most 10.
  - **Top deals:** the other passed deals, ordered by current temperature (unknown temperature last), then by `first_seen` newest first, at most `top` (default 10, 1–25).
  - Each deal appears in at most one digest per channel: a successful send records the listed deals in `digest_sent`. A deal that heats up later shows up in the next digest.
- **Nothing new:** when both lists are empty, nothing is sent, but the channel's `last_cutoff` still moves to `cutoff` (so tomorrow's window doesn't reach back).
- **Send test digest** (Settings page): composes the digest for the current window and sends it to the digest channels right away, without moving `last_cutoff` or recording any deal as sent. If the window is empty it sends a short "nothing new since …" message so the test shows something.

## Message

One message per channel. The title is `dealsieve · N new deals` (N = watchlist hits + top deals; singular "deal" for 1). The body is plain text:

```
★ Watchlist (2)
OLED TV 55" · 899€ · 312°
Steam Deck OLED · 449€ · 188°

Top deals
1. Ryzen 7 7800X3D · 299€ · 637°
2. Kindle Paperwhite · 99€ · 421°
```

- A section is left out when it is empty. Titles are cut to 80 characters with `…`; price and temperature are left out when unknown.
- **Telegram** gets an HTML version of the same body (`parse_mode: HTML`): each title is a link to its deal, and the text is HTML-escaped. If `DEALSIEVE_PUBLIC_URL` is set, a last line links to the inbox.
- **ntfy** gets the plain body. Its click URL is `DEALSIEVE_PUBLIC_URL` when set; otherwise the notification has no click target.
- Both stay under 4096 characters (Telegram's limit is characters, ntfy's is bytes): lines that don't fit are dropped from the end of the top list and replaced by `… and K more`.

## Settings

New top-level key, part of the versioned settings document:

```json
"digest": { "enabled": false, "time": "18:00", "timezone": "Europe/Berlin", "top": 10, "channels": [] }
```

- **Defaults** as above (off by default, so existing installs don't change).
- **Validation:** `time` is `HH:MM` 00:00–23:59; `timezone` loads with `time.LoadLocation` (the binary embeds `time/tzdata`, since the distroless image has no zoneinfo); `top` is 1–25; every channel exists in `channels`; when `enabled`, at least one channel. Errors are keyed by field path like the other settings (`digest.time`, …).
- **Summary line** (history): `Daily digest at 18:00 (Europe/Berlin) to phone, top 10` / `Daily digest off`.
- **Settings page:** a "Daily digest" block under Notifications: on/off, time, time zone (text field with a datalist of common zones), top N, channel checkboxes, "Send test digest" button with the same result display as "Send test".
- **Removing a channel** that the digest uses is a validation error, the same as for notify rules.

## Environment

- `DEALSIEVE_PUBLIC_URL` (optional): the address people open dealsieve at, e.g. `https://deals.example.com`. Used only as the digest's link to the inbox. Must be an absolute `http(s)` URL. Goes in `config.FromEnv`, the README table and the docs-site configuration page.

## Architecture

- **`internal/digest`** (new):
  - `Due(now time.Time, cfg settings.Digest, lastCutoff time.Time) bool` — pure.
  - `Compose(hits, top []Item, publicURL string) notify.Message` — pure; `Item` is title, URL, price, temperature.
  - `Runner` — every minute: for each digest channel that is due, select, compose, send, and on success record the listed deals and the new `last_cutoff` in one transaction (`CompleteDigest`). A channel that can't send (e.g. Telegram without a token) is logged at most once per channel per local day. Reads the current settings through the settings service's snapshot, builds its senders from them with `notify.FromSettings`, and sends **outside** the pipeline mutex, so a slow target never holds up polling. A failed send stores nothing, so the next tick retries; failures are logged with `notify.Reason`.
- **`internal/store`:**
  - migration `003_digest.sql`: `digest_state (channel TEXT PRIMARY KEY, last_cutoff INTEGER NOT NULL)` and `digest_sent (channel, deal_id → deals ON DELETE CASCADE, sent_at, PRIMARY KEY (channel, deal_id))`.
  - `DigestDeals(ctx, channel, since, top) (hits, others []DealRecord, error)`, `CompleteDigest(ctx, channel, dealIDs, cutoff)` (after a successful send), and `DigestCutoff` / `SetDigestCutoff` (the latter only for an empty window).
- **`internal/notify`:** `Message` gains `HTML string` (optional). Telegram sends it with `parse_mode: HTML` when set and falls back to the plain text otherwise; ntfy ignores it.
- **`internal/api`:** `POST /api/settings/digest/test` → `{ "results": [{ "channel": "...", "ok": true, "error": "" }] }`, using the same error wording as the channel test (no response bodies). It uses the saved settings; with no digest channels it answers `422` with a hint to add one and save.
- **`cmd/dealsieve`:** starts the runner with the poller; it stops on shutdown.

## Testing

- `Due`: table tests for before/at/after send time, a successful digest already today, catch-up after downtime the same day, a missed day, and both daylight-saving switches in Europe/Berlin.
- `Compose`: both sections, empty sections left out, title cut, unknown price/temperature, HTML escaping, the inbox link with and without `DEALSIEVE_PUBLIC_URL`, the 4096 cap with `… and K more`.
- Store: `last_seen` bound (exclusive), only `pass`, a deal first seen long before the window but passing and seen since is included, deals already sent to the channel are excluded (but not for another channel), watchlist split, ordering, limits; `CompleteDigest` is idempotent; pruning a deal removes its `digest_sent` rows; cutoff round trip.
- Runner: fake clock + fake notifiers — sends once per day per channel, a failing channel retries without resending to a working one, an empty window moves the cutoff without sending, a test send doesn't move it or mark deals sent, a deal first seen before the previous digest that passes later appears in the next one, a deal listed yesterday that is still active isn't listed again, a channel without a sender is warned about once a day.
- Settings: defaults, validation errors per field, summary lines. API: the test endpoint. Node tests for the new settings form model.

## Out of scope

Weekly digests, several digests a day, per-deal buttons in ntfy, deduplicating against deals already pushed instantly.
