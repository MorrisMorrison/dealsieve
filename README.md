# dealsieve

A small, self-hosted filter for deal feeds. dealsieve reads a deal community's feeds, throws away
the noise, and shows what's left on a simple web page, with optional push notifications.

Its first (and so far only) source is [mydealz](https://www.mydealz.de). dealsieve is not affiliated
with it; it reads the public RSS feeds, links every deal back to the original post, and is meant for
personal use. Please respect the site's terms and keep polling intervals reasonable.

## Screenshots

<!-- Generated from made-up demo data by the "README screenshots" workflow
     (.github/workflows/screenshots.yml, capture script in screenshots/) and published
     to the `screenshots` branch on every relevant push to main. Do not edit by hand. -->

<table align="center">
  <tr>
    <td align="center"><img src="https://raw.githubusercontent.com/MorrisMorrison/dealsieve/screenshots/inbox-light.png" width="420" alt="The inbox in the light theme: a list of passed deals on the left, the selected deal with its heat curve and reasons on the right" /><br /><sub>Inbox, light</sub></td>
    <td align="center"><img src="https://raw.githubusercontent.com/MorrisMorrison/dealsieve/screenshots/inbox-dark.png" width="420" alt="The same inbox in the dark theme" /><br /><sub>Inbox, dark</sub></td>
  </tr>
  <tr>
    <td align="center"><img src="https://raw.githubusercontent.com/MorrisMorrison/dealsieve/screenshots/blocked.png" width="420" alt="The Blocked tab with a recurring pseudo-deal selected and the reason it was blocked" /><br /><sub>Blocked, with the reason why</sub></td>
    <td align="center"><img src="https://raw.githubusercontent.com/MorrisMorrison/dealsieve/screenshots/phone.png" width="150" alt="The deal list on a phone" /> <img src="https://raw.githubusercontent.com/MorrisMorrison/dealsieve/screenshots/phone-detail.png" width="150" alt="A deal's detail sheet on a phone" /><br /><sub>On a phone</sub></td>
  </tr>
</table>

## What it filters

- **Recurring pseudo-deals.** The same kind of offer posted again and again ("flights from €19.99")
  is recognized by a fingerprint of merchant + title words and blocked after it has been seen a
  few times.
- **Categories, merchants and keywords** you never want to see.
- **Cold deals** below a temperature you choose (per category if you like).
- **Your own feedback:** every deal's detail panel has *Hide*, *Junk*, *Mute shop* and
  *Mute category*. *Junk* blocks everything that looks like that deal from then on. Every click
  can be undone from the Rules view.

The page is an inbox: a list of deals on the left and the selected deal on the right, with its
heat curve and why it's there (or why it's blocked). Blocked deals stay visible in their own tab,
so you can see whether a rule is too strict. Use ↑/↓ to move through the list and Enter to open a
deal; on a phone, a deal opens as a full-screen sheet.

## Watchlist and notifications

Watchlist keywords are checked against every new deal as soon as it's posted, before it trends.
Notification rules send watchlist hits, or deals above a temperature, to
[ntfy](https://ntfy.sh) and/or Telegram.

## Run it

```bash
docker run -d --name dealsieve -p 8080:8080 \
  -v dealsieve-data:/data \
  -v "$PWD/config.yaml:/config/config.yaml:ro" \
  ghcr.io/morrismorrison/dealsieve:latest
```

Start from [`config.example.yaml`](config.example.yaml). The page has **no login**: keep it on a
private network or put it behind your reverse proxy's authentication.

With a bind mount instead of a named volume for `/data`, make the directory writable by UID 65532.

## Configuration

| Env var | Default | |
|---|---|---|
| `DEALSIEVE_CONFIG` | `config.yaml` (`/config/config.yaml` in the image) | Path to the YAML config |
| `DEALSIEVE_DATA_DIR` | `./data` (`/data` in the image) | Where the SQLite database lives |
| `DEALSIEVE_LISTEN` | `:8080` | Listen address |
| `DEALSIEVE_LOG_FORMAT` | `text` | `text` or `json` |
| `DEALSIEVE_TELEGRAM_TOKEN` | | Bot token, needed for Telegram channels |
| `DEALSIEVE_NTFY_TOKEN` | | Access token for a protected ntfy server |
| `DEALSIEVE_MYDEALZ_URL` | `https://www.mydealz.de` | Feed host (for tests) |

Config changes apply on restart. Invalid configs are rejected at startup with a list of problems.

## Development

```bash
go test ./...
go test -tags e2e ./test/e2e/
node --test web/static/js/*.test.js
```

The frontend is plain HTML, CSS and ES modules embedded into the binary: no build step.

## License

MIT
