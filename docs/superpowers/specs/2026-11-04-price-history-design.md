# Price history — design

Status: approved, being implemented.

## Goal

When a deal comes in, show whether it is really a good price compared with what dealsieve has already seen for the same product: "Lowest seen: 179€ at Example Shop, Aug 2026 · €40 more than now" or "Best price in 180 days". It uses only dealsieve's own stored deals (kept 180 days) and nothing else. It is **information only**: it never changes a verdict, a score, a notification or the digest.

## Same product

Two deals are the same product when all of these hold:

Before splitting, text in square brackets (a shop prefix like `[MediaMarkt]`), prices and percentages in the title (`£799.99`, `999€`, `19,99 Euro`, `20%`) are dropped, and a `+` right after a letter or digit becomes the word `plus` (`S24+` → `s24 plus`).

1. **Model codes:** both titles have model codes and the sets are identical. A model code is a token with letters and digits that isn't a quantity (`s24`, `7800x3d`, `wh1000xm5`, `rtx5080`), the same tokens the fingerprint keeps since PR #25. A number with 4+ digits followed by `k` is a model code too (`14700k`), not a size. **Spaced codes, as a fallback:** only if a title has no model code this way, a word (letters only, ≥ 2 letters, not a filler word) directly followed by a number of 1–5 digits that is not itself followed by a unit word becomes one code (`iPhone 15 Pro` → `iphone15`, `RTX 5080` → `rtx5080`). A deal without a model code gets no price history. A bundle (`s24` + `buds3`) only matches the same bundle.
2. **Distinguishing words:** both titles have the same set of variant, accessory and condition words (multilingual list in `product`: `pro max ultra plus mini lite slim fe se air xl`, `hülle case cover coque funda etui panzerglas schutzfolie controller kabel adapter ladegerät …`, `refurbished generalüberholt gebraucht used renewed reconditionné reacondicionado odnowiony …`). So `S24` ≠ `S24 Ultra` ≠ `S24+` ≠ `S24 Hülle`, and `PS5` ≠ `PS5 Pro`.
3. **Sizes:** a conflict only when both titles name a size with the same unit and share no value for it. A size is a number with a unit (`128gb`, `2tb`, `65zoll`, `1000w`, `4x10`). A number followed by a separate size unit is joined (`16 GB` → `16gb`). So `128GB` ≠ `256GB` and `55 Zoll` ≠ `65 Zoll`, but an extra `139 cm`, `120Hz` or `8GB RAM` on one side doesn't block a match. Money and time units (`eur`, `euro`, `ct`, `uhr`, `h`, `min`, `s`, `d`, `tage`, `jahre`, `monate`) are not sizes. Plain numbers are not sizes either, since titles often carry the price. `3g 4g 5g 4k 8k` are features, not sizes.
4. **Other words:** the cleaned title words without model codes, distinguishing words, joined unit words and filler words overlap enough. Filler words are `product`'s own list on top of the fingerprint's German/English stopwords: French, Spanish, Polish and Italian function words (`pour de la avec para con el za na dla …`) and marketing words (`bestpreis tiefstpreis angebot deal preis neu new günstig …`). Overlap is shared words ÷ the size of the smaller set, and it must be ≥ 0.6. If either set is empty, this check passes. So "Galaxy S24 128GB" matches "Samsung Galaxy S24 Smartphone 128GB Onyx".

Known limits: a word-subset can still match a special edition of the same product ("Elden Ring PS5" vs "Elden Ring … Edition PS5"); the detail panel lists every matched deal so such a match is visible.

The shop doesn't matter: "lowest seen" is across all shops. Every stored deal counts, passed or blocked, from either feed.

## Prices

`deals.price` is the source's text (`9€`, `1.449€`, `0,01€`; future Pepper sites: `£12.99`, `£1,299.99`, `12,99 €`). `ParsePrice` turns it into cents plus an ISO currency:

- Currency from the symbol or code anywhere in the text: `€`/`EUR` → `EUR`, `£`/`GBP` → `GBP`, `$`/`USD` → `USD`, `zł`/`PLN` → `PLN`. Any other currency gives no price.
- Space thousands separators (dealabs `1 504,89€`, pepper.pl `118 500zł`; plain, no-break or narrow no-break space before exactly three digits) are removed first.
- Number: the first run of digits with `.` and `,`. If the last separator is followed by exactly 1–2 digits it is the decimal separator; any other separators are thousands separators. So `1.449` → 1449.00, `1,299.99` → 1299.99, `0,01` → 0.01, `12,5` → 12.50, `1.000.000` → 1,000,000.00.
- No price: no currency, no digits, a result ≤ 0 (freebies, `Gratis`, `0€`), or anything else that doesn't parse. Such deals get no history and don't count as anyone's history.
- Only deals in the same currency are compared.

## Data

Migration `004_price_history.sql` (renumbered at merge time if another branch takes 004):

```sql
CREATE TABLE deal_products (
    deal_id  TEXT PRIMARY KEY REFERENCES deals (id) ON DELETE CASCADE,
    models   TEXT NOT NULL,   -- sorted distinct model codes, space-separated
    currency TEXT NOT NULL,
    cents    INTEGER NOT NULL
);
CREATE INDEX deal_products_models ON deal_products (models, currency);
```

- A row exists only for deals with model codes and a parsed price. Sizes and words are recomputed from the title when needed, since only the model codes are needed to find candidates.
- **Ingest:** after `UpsertDeal`, the pipeline calls `Store.SetProduct` with the deal's index (or deletes its row when it has none, e.g. its price became unparseable).
- **Startup:** `Store.ReindexProducts(ctx, index func(title, price string) (models, currency string, cents int64, ok bool))` rebuilds the table from all deals in one transaction (it runs next to `Rekey`). This is also the backfill for deals stored before this feature, and it means a parser fix reaches old deals on the next start.
- Pruning deletes rows through the foreign key.

## Architecture

- **`internal/product`** (new, pure):
  - `ParsePrice(text string) (cents int64, currency string, ok bool)`.
  - `Of(title string) Info` with `Models`, `Sizes`, `Words []string` (sorted, distinct).
  - `Index(title, price string) (models, currency string, cents int64, ok bool)` for the store.
  - `Same(a, b Info) bool`: the three rules above.
  - `Compare(deal Candidate, candidates []Candidate) *History`: drops the deal itself and the candidates that aren't `Same` or are in another currency, then summarises. It returns nil when nothing matches.
  - It uses the fingerprint tokenizer, so `internal/fingerprint` exports the small helpers it needs (`IsQuantity`, `IsUnit`), with no change to `CleanTitle` or `Key` and so no fingerprint migration.
- **`internal/store`:** `SetProduct`, `DeleteProduct`, `ReindexProducts`, and `ProductCandidates(ctx, models []string) ([]ProductRow, error)`. The last returns every deal whose `models` is in the list, with id, url, title, merchant, price text, first seen, currency and cents. The store stays free of domain logic, the same way `Rekey` takes its key function.
- **`internal/api`:** `listDeals` looks up the listed deals' rows, makes one `ProductCandidates` call for their distinct model keys, and adds `price_history` per deal from `product.History`.

## API

`GET /api/deals`: each deal gets an optional `price_history` (left out when there's none):

```json
"price_history": {
  "status": "above",
  "currency": "EUR",
  "diff_cents": 4000,
  "count": 3,
  "lowest": { "id": "mydealz:123", "url": "…", "merchant": "Example Shop", "price": "179€", "first_seen": "2026-08-14T09:12:00Z" },
  "matches": [ { "id": "…", "url": "…", "title": "…", "merchant": "…", "price": "179€", "first_seen": "…" } ]
}
```

- `status` is `best` when this deal's price ≤ the lowest other match (ties count as best, `diff_cents` 0); otherwise `above` with `diff_cents` = this price − lowest.
- `count` is the number of other matching deals. `matches` holds up to 10 of them, cheapest first (ties: newest first). `lowest` is `matches[0]`.

## Page

- **List chip** (a second chip after the existing block/watchlist chip): `Best price` (green, `--ok`) for `best`, or `+40 €` (muted) for `above`. Nothing without history. Blocked deals get it too.
- **Detail panel**, a "Price history" section under the temperature:
  - `best`: "Best price in 180 days · seen N× before".
  - `above`: "Lowest seen: 179€ at Example Shop, Aug 2026 · 40 € more than now".
  - A `<details>` "Matched deals (N)" lists the matches (price, shop, month and year, title linked to the deal), so the match is always explainable. If N > 10, the list says "showing the 10 cheapest".
  - The section is hidden without history.
- Money is formatted in `format.js` by `money(cents, currency, locale)` with `Intl.NumberFormat` (the page passes the browser's locale; tests pass a fixed one). The lowest price is shown as the source wrote it. Months use `monthYear(iso, locale)`. Both get node tests.
- DOM is built with `textContent`, as everywhere else; titles are untrusted.

## Not in scope

Changing verdicts or scores; notification or digest lines; price changes inside one thread (the deal keeps its latest price); deals without model codes; currency conversion; a per-product page.

## Testing

- `product`: table tests for `ParsePrice` (€, £, thousands, decimals, freebies, garbage), `Of` (models, `16 GB` joining, money/time units excluded), `Same` (accessory, sizes both/one/none, bundles, other wording), and `History` (self excluded, currency mismatch, best/above/tie, cap at 10, ordering).
- `fingerprint`: the existing tests still pass unchanged.
- `store`: temp SQLite tests for `SetProduct`/`DeleteProduct`, `ReindexProducts` (backfill, rows dropped when they no longer index), `ProductCandidates`, and the cascade on prune.
- `pipeline`: ingesting a deal writes its product row.
- `api`: `/api/deals` with a fixed clock returns `price_history` for a matching pair and leaves it out otherwise.
- Frontend: `money`/`monthYear` node tests; a screenshot check of the chip and the panel on a local build.
- The e2e test stays green (its fixtures carry no model-code matches, so it only proves nothing breaks).
