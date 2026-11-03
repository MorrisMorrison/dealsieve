---
title: How it works
description: What dealsieve is, how a deal travels through the rules, and what every reason means.
---

dealsieve is a small, self-hosted filter for deal feeds. One Go binary polls a source's feeds, stores every deal in SQLite, runs each one through an ordered rule engine that records *why* it passed or was blocked, serves the result on a web page and can send push notifications.

Its first (and so far only) source is [mydealz](https://www.mydealz.de). dealsieve is not affiliated with it: it reads the public RSS feeds, links every deal back to the original post, and is meant for personal use. Please respect the site's terms and keep polling intervals reasonable.

## The feeds

| Feed | Default interval | Used for |
|---|---|---|
| `trending` | 5 minutes | Fills the page. Items carry a temperature. |
| `new` | 2 minutes | Every new deal, matched **only** against the watchlist. Items carry no temperature. |

A deal seen only in `new` appears on the page only if it matches the watchlist. Both intervals are configurable, with a minimum of one minute.

## The rule order

Each deal runs through these rules **in this order**. The first block reason wins and is listed first in the deal's reasons.

| # | Rule | Blocks when |
|---|---|---|
| 1 | Hidden | You clicked *Hide* on this deal. |
| 2 | Muted category | The category is in `muted_categories` or you clicked *Mute category*. |
| 3 | Muted merchant | The merchant is in `muted_merchants` or you clicked *Mute shop*. |
| 4 | Block keyword | The title contains a `block_keywords` entry. |
| 5 | Recurring | The same offer was seen `max_seen` times or more inside the recurrence window. Watchlist hits are exempt. |
| 6 | Like your junk | The deal has the same fingerprint as a deal you marked *Junk*. |
| 7 | Too cold | Its temperature is below `min_temperature` (or the category override). Watchlist hits are exempt. |
| 8 | Watchlist | Never blocks: marks the deal as a watchlist hit and boosts its score. |
| 9 | New-only | The deal was only seen in the `new` feed and is not a watchlist hit. |

If nothing blocks, the deal passes. Rule 9 results are not shown in the Blocked tab either, because those deals were never candidates for the page.

A passing deal records why it passed too, after the watchlist reason: its temperature against the minimum (`hot: 637° (minimum 100°)`, or `below the 100° minimum, kept for your watchlist`) and how often the offer was seen before (`new offer: not seen in the last 90 days` or `seen 1× before in the last 90 days (limit 2)`). Notifications list the reasons one per line.

Keywords (watchlist and `block_keywords`) are matched case-insensitively as a substring of the **full title**, so entries like `rtx 5080` work with their numbers.

## Recurring pseudo-deals

The worst noise is the same offer pattern posted every few weeks ("flights from 19.99") that looks cheap on price alone. dealsieve gives every deal a **fingerprint**: the merchant (or `-` if missing) plus the title's distinct words, sorted, after removing the temperature prefix, prices and other numbers, punctuation and common German and English stopwords. Re-posts of the same offer share a fingerprint.

The **recurrence count** is the number of *distinct* other deals with the same fingerprint whose first-seen time falls inside `rules.recurrence.window` (90 days by default). With `max_seen: 2`, an offer is blocked from the third time it shows up, with a reason such as `recurring: seen 3× before in 90 days (last 2026-10-12)`, where the number counts the earlier deals.

## The watchlist

Watchlist entries are checked against every new deal as soon as it is posted, before it trends. A hit:

- gets a score boost of +1000, so hits sort to the top,
- is exempt from the recurrence rule and the temperature rule on purpose, because the watchlist is explicit intent and fingerprints drop model numbers,
- is still blocked by hidden, muted category, muted merchant, block keyword and junk.

Hits are listed in their own tab and can trigger [notifications](/dealsieve/guide/notifications/).

## Your feedback

Every deal has buttons to open it, *Hide*, *Junk*, *Mute category* and *Mute shop*. *Junk* blocks everything that looks like that deal (same fingerprint) from then on. Every action can be undone.

After any action, verdicts of the last 7 days are recomputed (and at startup, so config changes apply). Recomputing never sends notifications.

## The page

The page is an inbox: a list of deals, with the selected deal and the reason for every decision in a detail panel. Tabs for passed deals, watchlist hits and blocked deals show their counts. Feed health sits in a status line in the header and turns red when a feed has not been polled successfully for three times its interval. The active rules and your actions are behind a Rules button. On phones the detail opens as a full-screen sheet. Keyboard: ↑/↓ or j/k move through the list, Enter opens a deal, Esc closes the sheet. It follows your system's light or dark theme.

The page has **no login**. Keep it on a private network or behind a reverse proxy that authenticates.

## What dealsieve does not do

- It does not detect fake "before" prices, since the feeds carry no old price.
- It has no LLM judgement yet (there is a placeholder interface).
- It does not reload its config while running; changes apply on restart.
