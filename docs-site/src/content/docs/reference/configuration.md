---
title: Configuration reference
description: Every setting on the Settings page and every environment variable.
---

dealsieve is configured in two places:

- The **Settings page** (the gear in the header) holds everything about filtering and notifications. Changes apply immediately, without a restart, and the last 7 days of verdicts are re-checked after a save.
- **Environment variables** hold infrastructure and secrets. They are read at startup.

There is no config file. The YAML file and its path variable from v0.0.1 are no longer read.

## Settings page

Saving validates the whole page at once and reports every problem under its field. Every save is kept as a version (see [History and undo](#history-and-undo)). Unsaved changes show a bar with **Discard** and **Save**.

### Watchlist

| Setting | Default | Meaning |
|---|---|---|
| Watchlist | empty | Keywords. A deal whose title contains one (case-insensitive) is a watchlist hit. |

### Filters

| Setting | Default | Validation |
|---|---|---|
| Minimum temperature | `100` | Whole number, may be negative. Deals colder than this are blocked. Unknown temperatures and watchlist hits are exempt. |
| Per-category minimums | none | Category name plus its own minimum (whole number). The page offers the categories it has seen. |
| Muted shops | empty | Merchants that are always blocked. |
| Muted categories | empty | Categories that are always blocked. |
| Block keywords | empty | Blocked when the title contains one (case-insensitive substring). |

The order in which these rules apply is described in [How it works](/dealsieve/guide/how-it-works/#the-rule-order).

### Recurring deals

| Setting | Default | Validation |
|---|---|---|
| Window (days) | `90` | 1 to 180. Deals are kept 180 days, so a longer window would silently under-count. |
| Max seen | `2` | At least 1. An offer seen this many times before is blocked, so `2` blocks the third appearance. |

### Notifications

Channels and notify rules; see [Notifications](/dealsieve/guide/notifications/) for how they are used.

| Setting | Validation |
|---|---|
| Channel name | Not empty, only `a-z`, `0-9` and `-`, unique. |
| Channel type | `ntfy` or `telegram`. |
| ntfy server | An http(s) URL with a host (required). |
| ntfy topic | Required. |
| ntfy priority | `min`, `low`, `default`, `high`, `urgent` or empty. |
| Telegram chat ID | Required. |
| Notify rule | Either "a watchlist deal passes" or "a deal reaches N°" with N above 0; at least one channel; every channel must exist. |

A Telegram channel while `DEALSIEVE_TELEGRAM_TOKEN` is unset is not an error: the page shows a warning on the channel, and sends fail (logged and retried like any other failure).

### Suggestions

| Setting | Default | Validation |
|---|---|---|
| Minimum count | `3` | At least 2. How many distinct deals from one shop or category you must hide or junk. |
| Window (days) | `30` | 1 to 180. How far back those clicks are counted. |

### List entries

List entries are trimmed, must not be empty and are de-duplicated case-insensitively; de-duplication is a normalisation on save, not an error.

### History and undo

Every save is stored as a version with a summary of what changed (for example `Added watchlist "lego"` or `Minimum temperature 120° → 100°`). A save with no differences is not stored. The History section lists the last 20 versions, and **Undo last change** saves the previous version again as a new one. If another browser tab saved in the meantime, the save fails with a conflict and the page offers to reload.

## Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `DEALSIEVE_DATA_DIR` | `./data` (`/data` in the image) | Where the SQLite database lives |
| `DEALSIEVE_LISTEN` | `:8080` | Listen address |
| `DEALSIEVE_LOG_FORMAT` | `text` | `text` or `json` |
| `DEALSIEVE_POLL_TRENDING` | `5m` | Interval for the feed that fills the page. Minimum `1m`. |
| `DEALSIEVE_POLL_NEW` | `2m` | Interval for the feed of every new deal (watchlist matching only). Minimum `1m`. |
| `DEALSIEVE_TELEGRAM_TOKEN` | | Bot token, needed for Telegram channels |
| `DEALSIEVE_NTFY_TOKEN` | | Access token for a protected ntfy server |
| `DEALSIEVE_MYDEALZ_URL` | `https://www.mydealz.de` | Feed host (for tests) |

Secrets live only in environment variables, never in the database or on the page. Invalid values (an interval below one minute, a log format other than `text` or `json`) stop the start-up with a list of problems.

## Upgrading from v0.0.1

The YAML config file is no longer read and is not imported. Remove its mount and the path variable, start the new version, and re-enter your settings on the Settings page. Poll intervals that you had in the file are now the `DEALSIEVE_POLL_*` variables. Mute clicks from before the upgrade are dropped; mute shops and categories again from a deal or on the page.
