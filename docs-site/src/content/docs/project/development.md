---
title: Development
description: Build, test and contribute to dealsieve.
---

dealsieve is a Go program using the standard library (`net/http`, `log/slog`, `database/sql`). The only direct dependencies are `modernc.org/sqlite` (pure Go) and `gopkg.in/yaml.v3`. The page is plain HTML, CSS and ES modules embedded into the binary: no framework, no npm packages, no build step.

## Commands

```bash
gofmt -l .                                  # must print nothing
go vet ./...
go test -race ./...                         # unit tests
go test -tags e2e ./test/e2e/               # builds the binary, runs it against fake servers
node --test web/static/js/*.test.js         # frontend unit tests (Node 24, no packages)
CGO_ENABLED=0 go build ./cmd/dealsieve      # must stay CGO-free
```

To run it, copy `config.example.yaml` to `config.yaml`; it writes `./data/dealsieve.db`. Both are git-ignored.

## Layout

| Path | Responsibility |
|---|---|
| `cmd/dealsieve/` | Wiring only, plus the `healthcheck` subcommand |
| `internal/config/` | YAML config and `DEALSIEVE_*` env vars, defaults, validation |
| `internal/source/` | Deal and feed types; `mydealz/` is the RSS adapter |
| `internal/fingerprint/` | Title cleaning and the fingerprint key |
| `internal/rules/` | The rule engine: a pure function from deal and facts to a result |
| `internal/pipeline/` | Ingest, recompute and prune |
| `internal/poller/` | One loop per feed with backoff and status |
| `internal/notify/` | ntfy and Telegram notifiers |
| `internal/store/` | SQLite and numbered migrations |
| `internal/api/` | The JSON API, `/health` and the embedded page |
| `web/` | The page |
| `test/e2e/` | End-to-end test |

## Conventions

- **Rule order is the contract.** A new rule needs a place in the order, a human-readable reason string and a table test.
- **Fingerprint changes are migrations:** recurrence counts and junk actions are keyed by the fingerprint.
- **Schema changes** go in a new numbered migration; never edit an applied one.
- **Every config setting** has a default, is validated, and is documented in `config.example.yaml` and the README.
- **Front-end:** build the DOM with `textContent` and attributes, never `innerHTML` with deal data; titles are untrusted.

## Tests stay offline

Nothing in the tests talks to mydealz, ntfy or Telegram. Sources and notifiers take a base URL and an HTTP client, and tests point them at `httptest` servers. Fixtures are made up; never copy real feed content into the repo. Time is injected instead of slept.

## Contributing

Every change starts from a GitHub issue, with one branch and one pull request per issue (`Closes #N` in the description). CI runs gofmt, vet, race tests, the end-to-end test, frontend tests and a Docker build on pull requests, and must be green before merging. Pushes to `main` publish the image to GHCR.

## This documentation site

The site lives in `docs-site/` (Astro and Starlight). The pages are Markdown files in `docs-site/src/content/docs/`; edit them there. To add a page, create the file and list it in the sidebar in `docs-site/astro.config.mjs`.

```bash
cd docs-site
npm ci
npm run dev       # live preview
npm run build     # also fails on a broken internal link
```

It is built on pull requests and deployed to GitHub Pages from `main`.
