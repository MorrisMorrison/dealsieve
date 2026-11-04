# More sources: other Pepper sites — design

Status: approved (issue #40).

## Goal

mydealz is dealsieve's first source. Other sites in the same network publish the same RSS format: hotukdeals (UK), dealabs (FR), preisjaeger (AT), chollometro (ES) and pepper.pl (PL). The user can turn each of them on next to (or instead of) mydealz on the Settings page. Every deal still links back to its own site, and the page says which site a deal came from.

## Live check (2026-11-04)

One request per feed with the dealsieve User-Agent. Every site answers `text/xml` with 30 items in the same structure as mydealz: `pepper:merchant` with `name`/`price` attributes, `media:content`, `<category>`, the `NNN° - ` temperature prefix on trending items only, and the thread id as the trailing `-<digits>` of the link. The existing `mydealz.Parse` read all twelve feeds with no item errors. Only these differ per site:

| Site (name) | Base URL | New-deals feed | Price format |
|---|---|---|---|
| `mydealz` | `https://www.mydealz.de` | `/rss/alles` | `7,87€`, `1.006€` |
| `hotukdeals` | `https://www.hotukdeals.com` | `/rss/new` | `£6.79`, `£40` |
| `dealabs` | `https://www.dealabs.com` | `/rss/tous` | `66,99€`, `1 504,89€` |
| `preisjaeger` | `https://www.preisjaeger.at` | `/rss/alle` | `41,37€`, `10€` |
| `chollometro` | `https://www.chollometro.com` | `/rss/nuevos` | `16,80€`, `279€` |
| `pepperpl` (shown as "pepper.pl") | `https://www.pepper.pl` | `/rss/wszystkie` | `309,03zł`, `118 500zł` |

Every site serves the trending feed at `/rss/trending`. `/rss/new` redirects to the new-deals paths above (except on hotukdeals, where it is the feed), so dealsieve requests the final paths and never relies on a redirect.

## Behaviour

- **Choosing sources:** a Sources card on the Settings page lists the six sites with a checkbox each. mydealz is on by default, the others are off. At least one source must stay on. Changes are versioned, undoable and applied without a restart, like every other setting.
- **Polling:** every enabled source polls its trending feed every `DEALSIEVE_POLL_TRENDING` and its new-deals feed every `DEALSIEVE_POLL_NEW`, each loop with its own backoff. The sites are separate hosts, so no host gets more requests than mydealz gets today. Every request sends the same `dealsieve/<version> (+repo URL)` User-Agent.
- **Turning a source off** stops its loops. Its deals drop out of the inbox, the view counts, the categories list, the daily digest and notification retries. The rows stay in the database until the normal 180-day prune, so turning the source back on shows them again.
- **Deal identity:** IDs are `<source>:<thread id>` (`hotukdeals:5022299`) and `source` is the site name. Thread ids overlap across sites, and the prefix keeps them apart. Existing `mydealz:` rows stay valid, so no migration is needed.
- **Recurrence is per source:** a deal only counts earlier deals with the same fingerprint *from the same source*. A cross-post on another site isn't a re-post by the shop.
- **Shared across sources:** Junk clicks (stored by fingerprint, so a junked offer stays junk on every site), Hide, the watchlist, muted categories and merchants, block keywords and category overrides.
- **Temperature threshold per source:** each source has an optional `min_temperature`. The threshold for a deal is its category override if one exists, else its source's `min_temperature` if set, else the global `rules.min_temperature`. When the source value decides, the reason says so: `too cold: 80° < 150° (hotukdeals minimum)` and `hot: 210° (hotukdeals minimum 150°)`. Other reasons don't change, and neither does the rule order.
- **Prices** stay the feed's own string (`£6.79`, `1 504,89€`, `999zł`) and are shown as is. dealsieve doesn't parse or convert them here (that is the price history work).
- **Which site:** when more than one source is on, each deal row and the detail panel show a small site label ("hotukdeals"), and notifications and the digest name the site (`hotukdeals · …`). With one source on, nothing changes on the page or in messages.
- **Source filter:** when more than one source is on, the inbox toolbar gets a source select ("All sources", then each enabled site). It filters the list and the view counts. Like the view tabs, the choice lives only in the page (not in the URL or storage) and resets to "All sources" if that site is turned off.

## Settings

A new top-level key in the settings document, keyed by source name:

```json
"sources": {
  "mydealz":     { "enabled": true },
  "hotukdeals":  { "enabled": false, "min_temperature": 150 },
  "dealabs":     { "enabled": false },
  "preisjaeger": { "enabled": false },
  "chollometro": { "enabled": false },
  "pepperpl":    { "enabled": false }
}
```

- **Defaults:** all six names, mydealz enabled, `min_temperature` absent (`null` = use the global minimum). `settings.Decode` starts from the defaults, so a document saved before this change gets mydealz on and the rest off. A source added in a later version shows up off.
- **Validation:** only known names (`sources.<name>`: "unknown source"); at least one enabled (`sources`: "turn at least one source on"); `min_temperature`, when set, is ≥ 0 (`sources.<name>.min_temperature`).
- **Normalize:** lowercases and trims names. Every known source is filled in (off) so the document always lists all six.
- **Summary lines** (history): `Sources: mydealz, hotukdeals` when the set of enabled sources changed; `hotukdeals minimum 150°` / `hotukdeals minimum: global` when a source threshold changed.
- **Settings page:** a "Sources" card above the filter rules. One row per site: a checkbox with the site name and its domain (the domain is plain text, not a link), and a number field "min °" with the placeholder "global".

## Environment

`DEALSIEVE_MYDEALZ_URL` stays, and five base-URL overrides join it: `DEALSIEVE_HOTUKDEALS_URL`, `DEALSIEVE_DEALABS_URL`, `DEALSIEVE_PREISJAEGER_URL`, `DEALSIEVE_CHOLLOMETRO_URL`, `DEALSIEVE_PEPPERPL_URL`. They exist for tests (point a site at a fake server), default to the table above, and are documented in the README and the docs-site configuration page. `config.Env` holds them as a map from source name to base URL.

## Architecture

- **`internal/source/pepper`** replaces `internal/source/mydealz` (moved with `git mv`, fixtures included):
  - `Site{Name, Label, BaseURL, NewPath}` and `Sites()`, the table above in display order. `Lookup(name)`.
  - `Parse(r, site string)`: today's parser with the ID prefix and `Source` taken from the site name.
  - `Client{Site, BaseURL, UserAgent, HTTP, Log}` implements `source.Fetcher`: trending uses `/rss/trending`, new uses `Site.NewPath`. Status errors and the 5 MiB read limit stay as they are.
- **`internal/config`:** `SourceURLs map[string]string`, built from the defaults plus the `DEALSIEVE_<NAME>_URL` overrides (validated as absolute http(s) URLs). `MydealzURL` is removed.
- **`internal/settings`:** `Sources map[string]Source` with `Source{Enabled bool; MinTemperature *int}`, plus `Defaults`, `Normalize`, `Validate`, `Summarize` and `EnabledSources() []string` in table order and `SourceMinimums() map[string]int`. The known names and their display labels live in settings (`settings.SourceNames`, `settings.SourceLabel(name)`: `pepperpl` → `pepper.pl`, others unchanged). The pepper package's table is tested to match it, so settings doesn't import the source package.
- **`internal/rules`:** `rules.Deal` gains `Source`. `rules.New(r, watchlist, sourceMins map[string]int)` (only sources with a value). `minTemperatureFor(category, source)` returns the threshold and which kind decided, so the reason can name the source. Table tests cover every precedence case and both reason texts.
- **`internal/store`:**
  - `CountRecurrence` takes the source and adds `AND source = ?`. The existing `deals_fingerprint` index still narrows by fingerprint first, so no new index is needed.
  - `ListDeals`, `CountViews`, `Categories` and `DigestDeals` take a source filter, a slice of names (`source IN (…)`), so callers pass either the enabled sources or the one picked in the filter.
  - `PendingNotifications` is filtered by the pipeline, not the store (see below).
- **`internal/poller`:** loops keyed by `(source, feed)`. `New(fetchers map[string]source.Fetcher, ingest, intervals, log)` takes one fetcher per known source, and `SetSources(names []string)` starts loops for newly enabled sources and cancels loops for disabled ones (each loop runs under its own child context; `Run` waits for all of them on shutdown). `Status()` reports one row per running loop with a new `source` field. A disabled source's state is dropped.
- **`internal/pipeline`:** `ApplySettings` also stores the enabled sources. `evaluate` passes `rec.Source` to `CountRecurrence` and `rules.Deal`. `retryPending` skips deals whose source is off. `send` passes the source label to `notify.DealMessage` only when more than one source is enabled.
- **`internal/notify`:** `DealMessage` gains a `source` argument. When it isn't empty, the ntfy title and the first Telegram line start with `hotukdeals · `.
- **`internal/digest`:** each line gets the site label when more than one source is enabled. Selection is limited to enabled sources.
- **`internal/api`:** `/api/deals` accepts `?source=<name>` (an unknown or disabled name is a 400 `{"error": "unknown source"}`). Without it, the list covers all enabled sources. Each deal in the response gets `source` and `source_label`. `GET /api/settings` also returns `sites`: every known site as `{name, label, domain}` in table order, for the Sources card. `/api/status` returns the per-loop rows with `source`, plus `sources`: the enabled source names in table order. `/api/categories` covers enabled sources. Everything reads the enabled sources from `svc.Current().Settings`.
- **Wiring (`cmd/dealsieve/main.go`):** builds one `pepper.Client` per site from `env.SourceURLs`, gives them all to the poller, and calls `SetSources` with the stored settings before `Run`. The settings service applies to both the pipeline and the poller: `main.go` passes a small applier that calls `pipe.ApplySettings` and then `pl.SetSources`.
- **Web:** `render.js` shows the site label on rows and in the detail panel when more than one source is enabled (the inbox reads the enabled list from `/api/status`, which it already loads). `main.js` adds the filter select. `settings.js` adds the Sources card from the `sites` list in `GET /api/settings`. The page never hard-codes site names: labels come from the server (`source_label` on each deal, `label` in the status and site lists).

## Fingerprints

Titles now also come in French, Spanish and Polish, and there are more English ones. Common short function words from those languages join the one shared stopword list:

- French: `le la les du et pour avec au aux sur par chez dans une`
- Spanish: `el los las del con por para al`
- Polish: `na do dla od ze za po oraz lub przy`
- English: `by via per off`
- `de` and `en` (French and Spanish; in German titles they only appear as `Amazon.de`-style fragments)

Words already on the list (`des`, `in`, …) stay. No word that is a common product word in German or English is added.

This changes `CleanTitle`, so it is a **fingerprint migration**: the startup `Store.Rekey` re-keys every stored deal from its title. Junk clicks move to the new key of the latest deal they came from. `RekeyOld` handles junk keys whose deals were already pruned: it now also drops stopwords from the key's words. That keeps it idempotent, since keys made by the current rules contain no stopwords. Before merging, the old and new keys of the live mydealz titles fetched for this design are compared, and the PR lists what changed.

## Error handling

- A site that answers with a non-200 status or something that isn't a feed fails that loop only. It backs off (capped at 30 min) and shows in the status line as `<site> · <feed>`. The other sources keep polling.
- A settings save that turns every source off is rejected by validation, so the poller never has zero loops because of the settings.
- `SetSources` with an unknown name (only possible from unvalidated settings) logs and skips it.

## Testing

- **Fixtures:** one small hand-made feed pair per new site in `internal/source/pepper/testdata/<site>-trending.xml` and `<site>-new.xml`. They contain made-up deals only (`Example Shop`, `static.example.test`, `.example.test` deal links) in each site's price format, following the live structure but with no copied content. The mydealz fixtures stay.
- **pepper:** a table test over all six sites: the right path per feed (`httptest.Server` records it), ID prefix, `Source`, User-Agent, status errors. Plus a test that `pepper.Sites()` names match `settings.SourceNames`.
- **config:** defaults, overrides, an invalid URL.
- **settings:** defaults, a document without `sources` decodes to mydealz-only, unknown name, none enabled, negative temperature, normalize fills missing sites, summary lines.
- **rules:** threshold precedence (category > source > global) and the reason texts, in `rules_test.go`.
- **store:** recurrence ignores other sources; list, counts, categories and digest respect the source filter (temp SQLite).
- **poller:** `SetSources` starts and stops loops (fake fetchers, which record calls) and `Status` shows one row per running loop.
- **pipeline:** a deal from a turned-off source isn't retried; the source label is only on messages when more than one source is on.
- **fingerprint:** new stopwords are dropped; `RekeyOld` drops them from old keys; `Of` on a title without stopwords is unchanged.
- **api:** `?source=` filter, 400 on an unknown source, `source` in the JSON.
- **e2e:** the fake feed server also serves hotukdeals fixtures (`DEALSIEVE_HOTUKDEALS_URL`). The test turns hotukdeals on with `PUT /api/settings` and checks that its deals appear with `source: "hotukdeals"` and that `?source=hotukdeals` returns only those.
- **By hand, before the PR:** a local build with mydealz and hotukdeals on against the live feeds; screenshots of the label, the filter and the Sources card (light and dark, 375 px).

## Docs

README (sources, env table), docs site (a Sources section on the configuration and settings pages, the env vars), `CLAUDE.md` (layout `internal/source/pepper`, e2e note, "first source" wording kept: mydealz is the first source, the other Pepper sites are more sources), the `add-source` project skill (a new Pepper site is a row in the site table, a settings name and fixtures). The screenshot demo settings stay mydealz-only.

## Not in this change

- Sources outside the Pepper network.
- Converting or comparing prices across currencies.
- Per-source poll intervals or per-source watchlists and mutes.
- Detecting the same deal cross-posted on two sites (each shows separately, labelled).
