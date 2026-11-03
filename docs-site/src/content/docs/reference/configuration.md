---
title: Configuration reference
description: Every config key and environment variable.
---

dealsieve reads a YAML file (path in `DEALSIEVE_CONFIG`) plus a few environment variables. Unknown keys are errors, all problems are reported at once, and changes apply on restart. Secrets come **only** from environment variables, never from the YAML.

## Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `DEALSIEVE_CONFIG` | `config.yaml` (`/config/config.yaml` in the image) | Path to the YAML config |
| `DEALSIEVE_DATA_DIR` | `./data` (`/data` in the image) | Where the SQLite database lives |
| `DEALSIEVE_LISTEN` | `:8080` | Listen address |
| `DEALSIEVE_LOG_FORMAT` | `text` | `text` or `json` |
| `DEALSIEVE_TELEGRAM_TOKEN` | | Bot token, needed for Telegram channels |
| `DEALSIEVE_NTFY_TOKEN` | | Access token for a protected ntfy server |
| `DEALSIEVE_MYDEALZ_URL` | `https://www.mydealz.de` | Feed host (for tests) |

## `poll`

| Key | Default | Meaning |
|---|---|---|
| `poll.trending` | `5m` | Interval for the feed that fills the page. Minimum `1m`. |
| `poll.new` | `2m` | Interval for the feed of every new deal (watchlist matching only). Minimum `1m`. |

## `rules`

| Key | Default | Meaning |
|---|---|---|
| `rules.min_temperature` | `0` | Deals colder than this are blocked. Unknown temperatures and watchlist hits are exempt. |
| `rules.category_overrides` | none | Map of category to its own minimum temperature. |
| `rules.muted_categories` | none | Categories that are always blocked. |
| `rules.muted_merchants` | none | Merchants that are always blocked. |
| `rules.block_keywords` | none | Blocked when the title contains one (case-insensitive substring). |
| `rules.recurrence.window` | `90d` | How far back to look for the same offer. |
| `rules.recurrence.max_seen` | `2` | An offer seen this many times before is blocked, so `2` blocks the third appearance. At least 1. |

The order in which these rules apply is described in [How it works](/dealsieve/guide/how-it-works/#the-rule-order).

## `watchlist`

A list of keywords. A deal whose title contains one (case-insensitive) is a watchlist hit.

```yaml
watchlist: [oled, rtx 5080, lego technic]
```

## `notify`

A list of rules, each with a `when` and a list of `channels`.

| Key | Meaning |
|---|---|
| `when` | `watchlist`, or `{ min_temperature: N }` with N above 0. |
| `channels` | At least one channel name defined under `channels`. |

## `channels`

A map of channel name to settings.

| Key | Type | Meaning |
|---|---|---|
| `type` | both | `ntfy` or `telegram`. |
| `url` | ntfy | Server base URL (required). |
| `topic` | ntfy | Topic (required). |
| `priority` | ntfy | `min`, `low`, `default`, `high`, `urgent`; optional. |
| `chat_id` | telegram | Target chat (required). |

See [Notifications](/dealsieve/guide/notifications/) for how they are used.

## Validation

At startup these are errors: unknown keys, unknown channel types, notify rules naming channels that do not exist, intervals below one minute, a non-positive recurrence window or `max_seen` below 1, an unknown ntfy priority, a Telegram channel without `DEALSIEVE_TELEGRAM_TOKEN`, and a log format other than `text` or `json`.

## Example

```yaml
poll:
  trending: 5m
  new: 2m

rules:
  min_temperature: 100
  category_overrides:
    "Elektronik": 60
  muted_categories: ["Fashion & Accessoires", "Verträge"]
  muted_merchants: []
  block_keywords: [gratisprobe, gewinnspiel, cashback]
  recurrence:
    window: 90d
    max_seen: 2

watchlist: [oled, rtx 5080, lego technic]

notify:
  - when: watchlist
    channels: [ntfy-high]
  - when: { min_temperature: 500 }
    channels: [ntfy-default]

channels:
  ntfy-high:    { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: high }
  ntfy-default: { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: default }
```
