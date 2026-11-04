---
title: Notifications
description: Send watchlist hits and hot deals to ntfy or Telegram.
---

Notifications are optional. In the **Notifications** section of the Settings page you define **channels** (where to send) and **notify rules** (when to send to which channels). Changes apply when you save, without a restart.

For example:

- Channel `phone`: ntfy, server `https://ntfy.example.com`, topic `deals`, priority `high`.
- Channel `tg`: Telegram, chat ID `123456789`.
- Rule: when a watchlist deal passes, send to `phone` and `tg`.
- Rule: when a deal reaches 500°, send to `phone`.

## Conditions

| Rule | Fires for |
|---|---|
| A watchlist deal passes | A deal matching a watchlist entry, as soon as it is posted. |
| A deal reaches N° | A deal whose temperature reaches N (above 0). Deals are re-checked as they heat up, so the rule fires once the threshold is crossed. |

Only deals that **pass** the rules are sent.

## Delivery

- At most one successful send per deal and channel.
- A failed send is retried on later polls, up to 3 attempts.
- Recomputing verdicts after a click, a save on the Settings page or a restart never notifies.

## Send test

Each channel card has a **Send test** button. It sends a "dealsieve test notification" through the channel exactly as it is in the form, before you save, and shows the error (for example `ntfy: status 404`) if it fails.

## Tokens stay in the environment

Access tokens are never entered on the page and never stored in the database. They come from environment variables:

- `DEALSIEVE_NTFY_TOKEN` for a protected ntfy server.
- `DEALSIEVE_TELEGRAM_TOKEN` for Telegram.

:::caution
`DEALSIEVE_NTFY_TOKEN` is sent to the server of every ntfy channel, and by **Send test**. Anyone who can reach the page can point a channel at a server of their own and receive it, so keep the page on a private network or behind an authenticating proxy, and use an ntfy token that is scoped to the dealsieve topic only.
:::

## ntfy

A POST to `url/topic` with the deal title, a message (price, temperature, merchant, reason), a click action that opens the deal, the deal's image as attachment and the channel's priority.

| Field | Meaning |
|---|---|
| Server | Base URL of the ntfy server (required). |
| Topic | Topic to publish to (required). |
| Priority | `min`, `low`, `default`, `high` or `urgent` (optional). |

## Telegram

Sends the deal's image with a caption (title, price, temperature, reason, link) and falls back to a plain message without an image.

1. Create a bot with BotFather and put its token in `DEALSIEVE_TELEGRAM_TOKEN`.
2. Set the chat ID on the channel to the chat the bot should write to.

A Telegram channel without the token is not rejected: the page shows a warning on the channel, and sends fail until the token is set.
