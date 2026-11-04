---
title: LLM judge
description: An optional second opinion from a language model on the deals that passed your rules.
---

The LLM judge is optional and off by default. When it is on, a language model gives each deal that passed your rules a short opinion: a rating (`good`, `meh` or `bad`) and one sentence of reasoning. You see it next to the deal and in the daily digest, and you stay in charge of what the rules do.

## What it does and doesn't do

- **It only annotates.** An opinion never blocks or unblocks a deal and never triggers a notification. Instant pushes work exactly as before.
- **Passed deals only.** A deal whose current verdict is "blocked" is not sent to the model.
- **Recent deals only.** Only deals first seen in the last 24 hours are judged. Turning the judge on does not back-fill older deals.
- **Once per deal.** Each deal is judged once, newest first, at most 20 per minute. Opinions are kept with the deal.

Where the opinion shows up:

- A small marker in the deal list: `✓` good, `~` meh, `!` bad.
- A **Judge** section in the detail panel with the rating, the sentence and the model name.
- A `↳ rating: reason` line under each deal in the [daily digest](/dealsieve/guide/how-it-works/#daily-digest).

## Set it up

Turn it on in the **LLM judge** block of the Settings page (under Notifications). The server address and the API key come from environment variables, the model is chosen on the page.

### With OpenRouter (the default)

1. Create an API key with your provider and set it as `DEALSIEVE_JUDGE_API_KEY`. The default `DEALSIEVE_JUDGE_URL` is `https://openrouter.ai/api/v1`, so nothing else is needed.
2. On the Settings page, tick **Enabled** and enter a model id, for example one from OpenRouter's model list.
3. Press **Test judge**. It judges a made-up sample deal with the values currently in the form, before you save, and shows the opinion or the error. A test does not count toward the daily limit.
4. Save.

### With a local server

Any server that speaks the OpenAI chat-completions API works. dealsieve sends its requests to `<url>/chat/completions`. For example, for an Ollama server on your network:

```bash
DEALSIEVE_JUDGE_URL=http://ollama.example.test:11434/v1
```

No key is needed; leave `DEALSIEVE_JUDGE_API_KEY` unset. Enter the model name your server knows (for example the name of a pulled model).

### Settings

| Setting | Default | Validation |
|---|---|---|
| Enabled | off | |
| Model | empty | Required when enabled. No spaces, at most 200 characters. |
| Daily limit | `200` | 1 to 5000. See [Cost control](#cost-control). |
| Instructions | empty | At most 500 characters. Added to the prompt as "The user adds:". Use it to tune taste ("I only care about electronics") or to set the reply language ("Answer in German"). |

### Model notes

Requests use `max_tokens: 400` and `temperature: 0`, and ask for a short JSON answer. Some reasoning models spend their whole token budget on thinking and return no answer; if **Test judge** reports an empty or unparseable reply, pick a non-reasoning model id. A provider that does not accept the `temperature` parameter shows up as `HTTP 400` in the test.

## What leaves your machine

For each judged deal the model receives:

- the title, price, shop, category and temperature,
- your **Instructions** text.

Nothing else: no watchlist, no settings, no history, no other deals, no keys. The reply is stored as the opinion. With a local server, none of this leaves your network.

## Cost control

- **Daily limit.** At most this many calls in any rolling 24 hours. When it is reached the judge waits until older calls fall out of the window.
- **Failures count.** Every request that reached the server counts toward the limit, including failed ones, so a broken setup cannot run up a bill. An unreachable server uses none.
- **Retries and back-off.** If the server is busy (HTTP 429 or 5xx), times out or can't be reached, dealsieve pauses the judge for 10 minutes, then 20, 40 and at most 1 hour while it keeps failing. The deals keep waiting and don't lose an attempt,. Saving the settings ends the pause and makes the judge check for work right away, so fixing the model or the URL is retried at once. Bad replies and other errors are retried 10 minutes later, at most 3 attempts per deal; after that the deal stays without an opinion. Each request is cut off after 60 seconds.

`GET /api/status` shows the calls made in the last 24 hours and the last error.

## The key stays with its URL

The server address is an environment variable on purpose. The page has no login, so if the address were a setting, anyone who can reach the page could point it at a host of their own and receive your API key. As an environment variable, the key can only ever go to the server you configured. The key is never stored, logged or returned by the API; the page only shows whether one is set.

:::note
`DEALSIEVE_JUDGE_API_KEY` is sent to the configured server by every judge call and by **Test judge**. Use a key with a spending cap.
:::

See also the [configuration reference](/dealsieve/reference/configuration/) and the [JSON API](/dealsieve/reference/api/).
