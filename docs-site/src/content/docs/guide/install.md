---
title: Install and run
description: Run dealsieve with Docker and a YAML config file.
---

dealsieve ships as a small distroless image (static binary, non-root user) on the GitHub Container Registry.

## 1. Create a config

Start from [`config.example.yaml`](https://github.com/MorrisMorrison/dealsieve/blob/main/config.example.yaml) and save it as `config.yaml`. Every key is explained in the [configuration reference](/dealsieve/reference/configuration/). An empty file (all defaults) is valid.

## 2. Run the container

```bash
docker run -d --name dealsieve -p 8080:8080 \
  -v dealsieve-data:/data \
  -v "$PWD/config.yaml:/config/config.yaml:ro" \
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
      - ./config.yaml:/config/config.yaml:ro
    environment:
      # only if you configured a telegram channel
      DEALSIEVE_TELEGRAM_TOKEN: "${DEALSIEVE_TELEGRAM_TOKEN}"
    restart: unless-stopped
volumes:
  dealsieve-data:
```

## Image tags

| Tag | Published when |
|---|---|
| `latest`, `main-<sha>` | Every push to `main` |
| `X.Y.Z` | A `vX.Y.Z` tag (never moves `latest`) |

## Good to know

:::caution[No login]
The page has no authentication. Keep it on a private network or put it behind your reverse proxy's authentication.
:::

- **Data.** The SQLite database lives in `/data`. With a bind mount instead of a named volume, make the directory writable by UID 65532.
- **Config changes** apply on restart. An invalid config is rejected at startup with a list of all problems.
- **Health.** `GET /health` returns `ok`; the image's `HEALTHCHECK` uses it.
- **Retention.** Deals are kept for 180 days, then removed with their history.
- **Being a good citizen.** dealsieve sends an identifying `User-Agent`, backs off on errors (capped at 30 minutes) and links every deal to its original post. Keep poll intervals reasonable.
