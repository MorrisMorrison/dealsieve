---
title: Install and run
description: Run dealsieve with Docker and configure it on the Settings page.
---

dealsieve ships as a small distroless image (static binary, non-root user) on the GitHub Container Registry.

## 1. Run the container

```bash
docker run -d --name dealsieve -p 8080:8080 \
  -v dealsieve-data:/data \
  ghcr.io/morrismorrison/dealsieve:latest
```

Open http://localhost:8080. The first deals appear after the first poll.

With Docker Compose:

```yaml
services:
  dealsieve:
    image: ghcr.io/morrismorrison/dealsieve:latest
    ports: ["8080:8080"]
    volumes:
      - dealsieve-data:/data
    environment:
      # only if you add a telegram channel on the Settings page
      DEALSIEVE_TELEGRAM_TOKEN: "${DEALSIEVE_TELEGRAM_TOKEN}"
    restart: unless-stopped
volumes:
  dealsieve-data:
```

## 2. Configure it

Open the page and click the gear in the header to reach **Settings**: watchlist, muted shops and categories, block keywords, temperature limits, recurring-deal limits and notification channels. A fresh install starts with sensible defaults, and changes apply as soon as you save. See the [configuration reference](/dealsieve/reference/configuration/) for every setting.

## Image tags

| Tag | Published when |
|---|---|
| `latest`, `main-<sha>` | Every push to `main` |
| `X.Y.Z` | A `vX.Y.Z` tag (never moves `latest`) |

## Good to know

:::caution[No login]
The page has no authentication, and neither does the Settings page. Keep it on a private network or put it behind your reverse proxy's authentication. `DEALSIEVE_NTFY_TOKEN` is sent to the server of every ntfy channel, and by **Send test**. Anyone who can reach the page can point a channel at a server of their own and receive it, so keep the page on a private network or behind an authenticating proxy, and use an ntfy token that is scoped to the dealsieve topic only.
:::

- **Data.** The SQLite database, including your settings, lives in `/data`. With a bind mount instead of a named volume, make the directory writable by UID 65532.
- **Settings** apply immediately. Environment variables (poll intervals, secrets, listen address) are read at startup; see the reference.
- **Upgrading from v0.0.1.** The YAML config file is no longer read; re-enter your settings on the page.
- **Health.** `GET /health` returns `ok`; the image's `HEALTHCHECK` uses it.
- **Retention.** Deals are kept for 180 days, then removed with their history.
- **Being a good citizen.** dealsieve sends an identifying `User-Agent`, backs off on errors (capped at 30 minutes) and links every deal to its original post. Keep poll intervals reasonable.
