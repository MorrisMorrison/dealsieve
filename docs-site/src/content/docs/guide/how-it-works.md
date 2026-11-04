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

A deal seen only in `new` appears on the page only if it matches the watchlist. Both intervals are set with environment variables (`DEALSIEVE_POLL_TRENDING`, `DEALSIEVE_POLL_NEW`), with a minimum of one minute.

## Where settings live

Everything you tune (watchlist, mutes, keywords, temperature limits, recurrence, notifications) lives on the **Settings page** and is stored in the database as versioned settings. A save applies immediately: dealsieve swaps in the new rules and re-checks the last 7 days of deals, without restarting. Environment variables are only for infrastructure and secrets. See the [configuration reference](/dealsieve/reference/configuration/).

## The rule order

Each deal runs through these rules **in this order**. The first block reason wins and is listed first in the deal's reasons.

| # | Rule | Blocks when |
|---|---|---|
| 1 | Hidden | You clicked *Hide* on this deal. |
| 2 | Muted category | The category is in your muted categories (also where *Mute category* adds it). |
| 3 | Muted merchant | The merchant is in your muted shops (also where *Mute shop* adds it). |
| 4 | Block keyword | The title contains a block keyword. |
| 5 | Recurring | The same offer was seen the maximum number of times or more inside the recurrence window. Watchlist hits are exempt. |
| 6 | Like your junk | The deal has the same fingerprint as a deal you marked *Junk*. |
| 7 | Too cold | Its temperature is below the minimum temperature (or the category's own minimum). Watchlist hits are exempt. |
| 8 | Watchlist | Never blocks: marks the deal as a watchlist hit and boosts its score. |
| 9 | New-only | The deal was only seen in the `new` feed and is not a watchlist hit. |

If nothing blocks, the deal passes. Rule 9 results are not shown in the Blocked tab either, because those deals were never candidates for the page.

A passing deal records why it passed too, after the watchlist reason: its temperature against the minimum (`hot: 637° (minimum 100°)`, or `below the 100° minimum, kept for your watchlist`) and how often the offer was seen before (`new offer: not seen in the last 90 days` or `seen 1× before in the last 90 days (limit 2)`). Notifications list the reasons one per line.

Keywords (watchlist and `block_keywords`) are matched case-insensitively as a substring of the **full title**, so entries like `rtx 5080` work with their numbers.

## Recurring pseudo-deals

The worst noise is the same offer pattern posted every few weeks ("flights from 19.99") that looks cheap on price alone. dealsieve gives every deal a **fingerprint**: the merchant (or `-` if missing) plus the title's distinct words, sorted, after removing the temperature prefix, prices and other plain numbers, sizes and counts (`16GB`, `250ml`, `2x`, `120Hz`), punctuation and common German and English stopwords. Model codes that mix letters and digits (`S24`, `RTX5080`, `1000XM5`) stay, so different models from one shop get different fingerprints, while re-posts of the same offer, even at another price or size, share one.

The **recurrence count** is the number of *distinct* other deals with the same fingerprint that were first seen *before* this one, inside the recurrence window that ends when this deal was first seen (90 days by default). It only looks back, so a deal that passed stays passed when the same offer is posted again later; only the later re-posts are blocked. With a maximum of 2, an offer is blocked from the third time it shows up, with a reason such as `recurring: seen 3× before in 90 days (last 2026-10-12)`, where the number counts the earlier deals.

## The watchlist

Watchlist entries are checked against every new deal as soon as it is posted, before it trends. A hit:

- gets a score boost of +1000, so hits sort to the top,
- is exempt from the recurrence rule and the temperature rule on purpose, because the watchlist is explicit intent and fingerprints drop numbers such as sizes and storage,
- is still blocked by hidden, muted category, muted merchant, block keyword and junk.

Hits are listed in their own tab and can trigger [notifications](/dealsieve/guide/notifications/).

## Daily digest

Optionally, dealsieve sends one digest a day per channel, at the time and in the time zone you set. It lists the deals that passed, were active (seen in a feed) since the previous digest, and weren't in an earlier digest for that channel; a channel's first digest looks back 24 hours. Deals usually pass hours after dealsieve first sees them, so a deal that heats up after one digest is in the next. First comes a recap of your watchlist hits, newest first and at most 10, then the top N other passed deals by temperature. Each deal appears once per channel. When nothing is new, no digest is sent. If a send fails it is retried every minute; if it keeps failing, the next day's digest covers both days. The digest is separate from the instant notifications, which keep working as before.

## Your feedback

Every deal has buttons to open it, *Hide*, *Junk*, *Mute category* and *Mute shop*. *Junk* blocks everything that looks like that deal (same fingerprint) from then on. *Mute shop* and *Mute category* add the shop or category to your settings, so they show up in the Settings page and its history.

**Undo.** After *Hide* or *Junk*, the deal shows an undo link right away. Mutes are undone from the Settings page: edit the list, or use *Undo last change* in the History section.

**Suggestions.** When you hide or junk several deals from the same shop or category (three within 30 days by default), dealsieve suggests muting it, with the number of clicks and example titles. The inbox shows a banner when suggestions are waiting; on the Settings page you can **Apply** a suggestion (it becomes a mute in your settings) or **Dismiss** it for good. The thresholds are settings too. Suggestions are computed on demand from your clicks and never stored.

After any action or settings change, verdicts of the last 7 days are recomputed (and at startup). Recomputing never sends notifications.

## Price history

For a deal whose title has a model code (`S24`, `7800X3D`, or `iPhone 15` written with a space), dealsieve looks for other stored deals of the same product, at any shop, in the last 180 days: the same model codes; the same variant, accessory and condition words (`S24` is not `S24 Ultra`, `S24+`, a `S24 case` or a refurbished one; `PS5` is not `PS5 Pro`); no clash in size (`128GB` vs `256GB` never match, but an extra `139 cm` on one side is fine); and mostly the same other words. Prices in the title and shop prefixes like `[Shop]` are ignored. Prices are compared only within one currency.

The deal's panel then shows either **Best price in 180 days** or the lowest price seen ("Lowest seen: 549€ at Example Shop, Aug 2026 · now 40 € more"), with the matched deals listed so you can check them. The list shows a **Best price** or **+40 €** chip. Price history is information only: it never blocks or ranks a deal, and deals without a model code or a price have none.

## The page

The page is an inbox: a list of deals, with the selected deal and the reason for every decision in a detail panel. Tabs for passed deals, watchlist hits and blocked deals show their counts. Feed health sits in a status line in the header and turns red when a feed has not been polled successfully for three times its interval. The gear in the header opens the Settings page. On phones the detail opens as a full-screen sheet. Keyboard: ↑/↓ or j/k move through the list, Enter opens a deal, Esc closes the sheet. It follows your system's light or dark theme.

The page has **no login**. Keep it on a private network or behind a reverse proxy that authenticates.

## What dealsieve does not do

- It does not detect fake "before" prices, since the feeds carry no old price.
- It does not know prices outside its own feeds: price history only covers deals dealsieve stored itself, and only products with a model code.
- It has no LLM judgement yet (there is a placeholder interface).
- It does not hot-reload environment variables (poll intervals, secrets); those apply on restart.
