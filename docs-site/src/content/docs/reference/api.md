---
title: JSON API
description: The endpoints behind the web page.
---

The web page talks to the backend only through this JSON API. Errors are returned as `{"error": "..."}`, except validation failures (see below). There is no authentication; see [Install and run](/dealsieve/guide/install/).

| Method and path | Purpose |
|---|---|
| `GET /api/deals?view=passed\|watchlist\|blocked` | Deals seen in the last 3 days for one view (default `passed`). |
| `POST /api/actions` | Hide or junk a deal, or mute its shop or category. |
| `POST /api/actions/undo` | Undo a Hide or a Junk. |
| `DELETE /api/actions/{id}` | Remove an action by ID and recompute verdicts. |
| `GET /api/settings` | The current settings, version and channel warnings. |
| `PUT /api/settings` | Save new settings. |
| `POST /api/settings/undo` | Restore the previous settings version. |
| `GET /api/settings/history` | The last 20 settings versions. |
| `POST /api/settings/test` | Send a test notification through a channel. |
| `GET /api/suggestions` | Mute suggestions learned from Hide and Junk clicks. |
| `POST /api/suggestions/apply` | Accept a suggestion. |
| `POST /api/suggestions/dismiss` | Dismiss a suggestion for good. |
| `GET /api/categories` | Categories seen in stored deals. |
| `GET /api/status` | Per source and feed: last successful poll and last error, plus the enabled `sources`. |
| `GET /health` | Liveness for monitors. |

## `GET /api/deals`

Returns up to 200 deals, sorted by score and then by first-seen time. Each deal carries its verdict, the reasons, the current temperature and the temperature history (used for the sparkline). Add `source=<name>` (`mydealz`, `hotukdeals`, `dealabs`, `preisjaeger`, `chollometro` or `pepperpl`) to list one source only; an unknown `view` or `source` returns `400`. Every deal carries `source` and `source_label`.

The response also contains `counts` next to `deals`, with the number of deals per view (`passed`, `watchlist`, `blocked`) over the same window, whichever view you listed.

```json
{
  "deals": [ { "id": "mydealz:123456", "source": "mydealz", "source_label": "mydealz", "title": "...", "reasons": ["..."] } ],
  "counts": { "passed": 12, "watchlist": 2, "blocked": 40 }
}
```

## `POST /api/actions`

Body:

```json
{ "kind": "junk", "deal_id": "mydealz:123456" }
```

`kind` is one of `hide_deal`, `junk`, `mute_category` or `mute_merchant`. The server derives the value from the deal (the deal ID, its fingerprint, its category or its merchant), so clients cannot inject arbitrary values.

`hide_deal` and `junk` are stored as actions and answer `201` with the action. `mute_category` and `mute_merchant` instead add the deal's category or shop to the settings (a new settings version with the summary `Added muted shop "X" (from a deal)` or `Added muted category "X" (from a deal)`) and answer `201` with `{kind, value, version}`, plus a `warning` if re-checking recent deals failed.

## `POST /api/actions/undo`

Body `{ "kind": "hide_deal" | "junk", "deal_id": "..." }`. Removes the Hide on that deal, or the Junk mark on its fingerprint, recomputes verdicts and answers `204`. Other kinds return `400`; an unknown deal returns `404`. Mutes are undone with the settings undo or on the Settings page.

## `DELETE /api/actions/{id}`

Removes the action with that ID and recomputes verdicts. Answers `204`, or `404` for an unknown ID.

## `GET /api/settings`

```json
{
  "version": 3,
  "updated_at": "2026-11-04T10:15:00Z",
  "settings": {
    "watchlist": ["oled"],
    "sources": { "mydealz": { "enabled": true }, "hotukdeals": { "enabled": true, "min_temperature": 150 } },
    "rules": {
      "min_temperature": 100,
      "category_overrides": { "Elektronik": 60 },
      "muted_categories": [],
      "muted_merchants": ["Example Air"],
      "block_keywords": [],
      "recurrence": { "window_days": 90, "max_seen": 2 }
    },
    "channels": {
      "phone": { "type": "ntfy", "url": "https://ntfy.example.com", "topic": "deals", "priority": "high" },
      "tg": { "type": "telegram", "chat_id": "123" }
    },
    "notify": [ { "when": "watchlist", "channels": ["phone", "tg"] } ],
    "suggestions": { "min_count": 3, "window_days": 30 }
  },
  "warnings": { "tg": "DEALSIEVE_TELEGRAM_TOKEN isn't set on the server, so this channel can't send." }
}
```

`warnings` maps channel names to messages and is empty when there is nothing to warn about. Secrets never appear in the settings.

## `PUT /api/settings`

Body `{ "version": 3, "settings": { ... } }`, where `version` is the version you loaded. Unknown keys are rejected.

| Status | Meaning |
|---|---|
| `200` | Saved, applied and recent deals re-checked. The body is the `GET` body plus `rechecked` (number of deals re-checked) and `changed`. If re-checking failed after saving, the body also has a `warning` string; the settings are saved. |
| `400` | The body is not valid JSON or contains unknown keys. |
| `409` | `{"error": "..."}`: `version` is not the current one. Reload and try again. |
| `422` | `{"errors": {"rules.recurrence.window_days": "must be between 1 and 180 days"}}`: all validation problems, keyed by field path. |

A save with no differences is not stored as a new version.

## `POST /api/settings/undo`

Body `{ "version": 7 }`, the version the client is looking at. Saves the version before it as a new version (summary `Undid: <previous summary>`). Undoing twice therefore re-applies the change you just undid. Returns the same body as a successful `PUT`, including the optional `warning` when re-checking recent deals failed after the undo was saved.

| Status | Meaning |
|---|---|
| `200` | Undone. |
| `400` | The body is not valid JSON. |
| `409` | `{"error": "..."}`: `version` is not the current one, or there is no earlier version to go back to. |
| `500` | The undo could not be saved. |

## `GET /api/settings/history`

```json
{ "history": [ { "version": 3, "created_at": "2026-11-04T10:15:00Z", "summary": ["Added watchlist \"lego\""] } ] }
```

The last 20 versions, newest first.

## `POST /api/settings/test`

Body `{ "channel": { "type": "ntfy", "url": "https://ntfy.example.com", "topic": "deals" } }`. Sends "dealsieve test notification" through the channel exactly as given, so you can test a form before saving it.

| Response | Meaning |
|---|---|
| `200 {"ok": true}` | The message was sent. |
| `200 {"ok": false, "error": "ntfy: status 404"}` | Sending failed (only the status is shown, never the target's answer), or a Telegram channel has no token configured. |
| `422 {"errors": {...}}` | The channel itself is invalid. |

## `GET /api/suggestions`

```json
{
  "suggestions": [
    {
      "kind": "mute_merchant",
      "value": "Example Shop",
      "count": 4,
      "since": "2026-10-20T08:00:00Z",
      "examples": ["Example deal one", "Example deal two", "Example deal three"]
    }
  ]
}
```

`kind` is `mute_merchant` or `mute_category`. `count` is the number of distinct deals you hid or junked from that shop or category inside the suggestion window; `examples` has up to three titles. Shops and categories that are already muted or dismissed are not listed. Suggestions are computed on each request and never stored.

## `POST /api/suggestions/apply` and `/dismiss`

Body `{ "kind": "mute_merchant", "value": "Example Shop" }`. `apply` adds the value to the muted list (summary `Added muted shop "X" (suggested)`, or `Added muted category "X" (suggested)`) and recomputes verdicts; `dismiss` records a permanent dismissal. Both return the updated `{"suggestions": [...]}`, with a `warning` if re-checking failed after an apply. An invalid `kind` or an empty `value` returns `400`.

## `GET /api/categories`

`{"categories": ["Elektronik", "Fashion & Accessoires"]}`: the distinct categories in stored deals, for the category pickers.

## Safety

State-changing requests are protected against cross-origin use, and every response carries `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.
