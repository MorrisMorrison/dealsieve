---
title: JSON API
description: The endpoints behind the web page.
---

The web page talks to the backend only through this JSON API. Errors are returned as `{"error": "..."}`. There is no authentication; see [Install and run](/dealsieve/guide/install/).

| Method and path | Purpose |
|---|---|
| `GET /api/deals?view=passed\|watchlist\|blocked` | Deals seen in the last 3 days for one view (default `passed`). |
| `POST /api/actions` | Record a click action and recompute verdicts. |
| `DELETE /api/actions/{id}` | Undo an action and recompute verdicts. |
| `GET /api/rules` | The effective rules and the list of actions. |
| `GET /api/status` | Per feed: last successful poll and last error. |
| `GET /health` | Liveness for monitors. |

## `GET /api/deals`

Returns up to 200 deals, sorted by score and then by first-seen time. Each deal carries its verdict, the reasons, the current temperature and the temperature history (used for the sparkline). An unknown `view` returns `400`.

The response also contains `counts` next to `deals`, with the number of deals per view (`passed`, `watchlist`, `blocked`) over the same window, whichever view you listed.

```json
{
  "deals": [ { "id": "mydealz:123456", "title": "...", "reasons": ["..."] } ],
  "counts": { "passed": 12, "watchlist": 2, "blocked": 40 }
}
```

## `POST /api/actions`

Body:

```json
{ "kind": "junk", "deal_id": "mydealz:123456" }
```

`kind` is one of `hide_deal`, `junk`, `mute_category` or `mute_merchant`. The server derives the value from the deal (the deal ID, its fingerprint, its category or its merchant), so clients cannot inject arbitrary values.

## `DELETE /api/actions/{id}`

Removes the action with that ID and recomputes verdicts.

## Safety

State-changing requests are protected against cross-origin use, and every response carries `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.
