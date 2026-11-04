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

### Sources

| Setting | Default | Validation |
|---|---|---|
| Source on/off | mydealz on, the others off | One switch each for mydealz, hotukdeals, dealabs, preisjaeger, chollometro and pepper.pl. At least one must be on. |
| Minimum temperature (per source) | none | Optional whole number, 0 or more; empty means the global minimum. Used instead of the global minimum for deals from that source. |

Turning a source on starts polling its feeds right away; turning it off stops polling it and hides its deals (they are kept until the 180-day prune, so turning it on again brings them back). The minimum temperature applies in this order: the category's own minimum, then the source's minimum, then the global minimum. Reasons name the source's minimum, for example `too cold: 80° < 150° (hotukdeals minimum)` or `hot: 210° (hotukdeals minimum 150°)`.

With more than one source on, the inbox shows each deal's site, offers a source filter, and notifications and digest lines start with the site (`hotukdeals · <title>`). The sites use the same RSS format as mydealz and are polled with the same intervals and User-Agent. Prices are shown as the feed gives them (£, €, zł); nothing is converted.

### Filters

| Setting | Default | Validation |
|---|---|---|
| Minimum temperature | `100` | Whole number, may be negative. Deals colder than this are blocked. Unknown temperatures and watchlist hits are exempt. |
| Per-category minimums | none | Category name plus its own minimum (whole number). The page offers the categories it has seen. |
| Muted shops | empty | Merchants that are always blocked. |
| Muted categories | empty | Categories that are always blocked. |
| Block keywords | empty | Blocked when the title contains one (case-insensitive substring). |

The order in which these rules apply is described in [How it works](/dealsieve/guide/how-it-works/#the-rule-order). The watchlist, mutes, block keywords and category minimums apply to every source.

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

With `DEALSIEVE_NTFY_TOKEN` set, an ntfy channel also gets a warning when `DEALSIEVE_NTFY_TOKEN_HOST` is unset (the token goes to every ntfy server) or names another server (the token isn't sent to this one). Sends are not blocked by it.

A Telegram channel while `DEALSIEVE_TELEGRAM_TOKEN` is unset is not an error: the page shows a warning on the channel, and sends fail (logged and retried like any other failure).

### Daily digest

One message a day per channel with the deals that passed, were active since the previous digest and weren't in an earlier digest for that channel (the first digest looks back 24 hours); see [How it works](/dealsieve/guide/how-it-works/#daily-digest) for what it contains.

| Setting | Default | Validation |
|---|---|---|
| Enabled | off | |
| Time | `18:00` | `HH:MM`, 24-hour clock. |
| Time zone | `Europe/Berlin` | An IANA zone name such as `Europe/Berlin` or `UTC`. |
| Top | `10` | 1 to 25. How many deals follow the watchlist recap. |
| Channels | empty | At least one when the digest is enabled; every channel must exist. |

**Send test digest** sends a digest now, using the saved settings (not unsaved edits). It does not count as the day's digest and doesn't mark any deal as sent, so the scheduled one still goes out with the same deals.

### LLM judge

An optional model opinion on deals that passed; see [LLM judge](/dealsieve/guide/llm-judge/). The server address and key are environment variables (below).

| Setting | Default | Validation |
|---|---|---|
| Enabled | off | |
| Model | empty | Required when enabled. No spaces, at most 200 characters. |
| Daily limit | `200` | 1 to 5000. Calls per rolling 24 hours; failed calls count. |
| Instructions | empty | At most 500 characters. Appended to the prompt; can set the reply language. |

**Test judge** judges a made-up sample deal with the values in the form (unsaved edits included). It does not count toward the daily limit.

### Suggestions

| Setting | Default | Validation |
|---|---|---|
| Minimum count | `3` | At least 2. How many distinct deals from one shop or category you must hide or junk. |
| Window (days) | `30` | 1 to 180. How far back those clicks are counted. |

### List entries

List entries are trimmed, must not be empty and are de-duplicated case-insensitively; de-duplication is a normalisation on save, not an error.

### Your clicks

Lists your latest Hide and Junk clicks (up to 200, newest first) with the deal's title, linked to the post while the deal is still stored, and how long ago you clicked. **Undo** takes a single click back and re-checks the last 7 days of deals. Mutes are not listed here: they are settings, undone from their list or from [History and undo](#history-and-undo).

### History and undo

Every save is stored as a version with a summary of what changed (for example `Added watchlist entry "lego"`, `Added muted shop "Example Shop" (from a deal)` or `Minimum temperature 120° → 100°`). A save with no differences is not stored. The History section lists the last 20 versions, and **Undo last change** saves the version before the current one again as a new one, so undoing twice re-applies the change you just undid. If another browser tab saved in the meantime, a save or an undo fails with a conflict and the page offers to reload.

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
| `DEALSIEVE_NTFY_TOKEN_HOST` | | Host (optionally `host:port`, no scheme) the ntfy token may be sent to; other ntfy servers get no token. Unset: the token goes to every ntfy server |
| `DEALSIEVE_MYDEALZ_URL` | `https://www.mydealz.de` | Feed host (for tests or a mirror) |
| `DEALSIEVE_HOTUKDEALS_URL` | `https://www.hotukdeals.com` | Feed host (for tests or a mirror) |
| `DEALSIEVE_DEALABS_URL` | `https://www.dealabs.com` | Feed host (for tests or a mirror) |
| `DEALSIEVE_PREISJAEGER_URL` | `https://www.preisjaeger.at` | Feed host (for tests or a mirror) |
| `DEALSIEVE_CHOLLOMETRO_URL` | `https://www.chollometro.com` | Feed host (for tests or a mirror) |
| `DEALSIEVE_PEPPERPL_URL` | `https://www.pepper.pl` | Feed host (for tests or a mirror) |
| `DEALSIEVE_PUBLIC_URL` | unset | Optional. The address you open dealsieve at, as an absolute http(s) URL, e.g. `https://deals.example.com`. The daily digest uses it as its link to the inbox (the click target of the ntfy message, the last line of the Telegram message). |

| `DEALSIEVE_JUDGE_URL` | `https://openrouter.ai/api/v1` | Base URL of an OpenAI-compatible chat-completions API for the [LLM judge](/dealsieve/guide/llm-judge/); requests go to `<url>/chat/completions`. An environment variable on purpose: the page has no login, so the key can't be redirected to another host from the page. |
| `DEALSIEVE_JUDGE_API_KEY` | | Optional bearer key for the judge's server. Never stored, logged or returned; the page only shows whether one is set. |

Secrets live only in environment variables, never in the database or on the page. Invalid values (an interval below one minute, a log format other than `text` or `json`) stop the start-up with a list of problems.

## Upgrading from v0.0.1

The YAML config file is no longer read and is not imported. Remove its mount and the path variable, start the new version, and re-enter your settings on the Settings page. Poll intervals that you had in the file are now the `DEALSIEVE_POLL_*` variables. Mute clicks from before the upgrade are dropped; mute shops and categories again from a deal or on the page.
