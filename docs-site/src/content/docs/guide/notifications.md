---
title: Notifications
description: Send watchlist hits and hot deals to ntfy or Telegram.
---

Notifications are optional. You define **channels** (where to send) and **notify rules** (when to send to which channels).

```yaml
notify:
  - when: watchlist
    channels: [ntfy-high]
  - when: { min_temperature: 500 }
    channels: [ntfy-default, telegram]

channels:
  ntfy-high:    { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: high }
  ntfy-default: { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: default }
  telegram:     { type: telegram, chat_id: "123456789" }
```

## Conditions

| `when` | Fires for |
|---|---|
| `watchlist` | A deal matching a watchlist entry, as soon as it is posted. |
| `{ min_temperature: N }` | A deal whose temperature reaches N. Deals are re-checked as they heat up, so the rule fires once the threshold is crossed. |

Only deals that **pass** the rules are sent.

## Delivery

- At most one successful send per deal and channel.
- A failed send is retried on later polls, up to 3 attempts.
- Recomputing verdicts after a click or a restart never notifies.

## ntfy

A POST to `url/topic` with the deal title, a message (price, temperature, merchant, reason), a click action that opens the deal, the deal's image as attachment and the channel's `priority`.

| Key | Meaning |
|---|---|
| `url` | Base URL of the ntfy server (required). |
| `topic` | Topic to publish to (required). |
| `priority` | `min`, `low`, `default`, `high` or `urgent` (optional). |

For a protected server set the access token in `DEALSIEVE_NTFY_TOKEN`.

## Telegram

Sends the deal's image with a caption (title, price, temperature, reason, link) and falls back to a plain message without an image.

1. Create a bot with BotFather and put its token in `DEALSIEVE_TELEGRAM_TOKEN`.
2. Set `chat_id` on the channel to the chat the bot should write to.

A Telegram channel without the token is a startup error.
