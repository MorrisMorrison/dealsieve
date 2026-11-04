-- One row per deal with a model code and a parsed price, for price history.
-- models: the title's model codes, sorted, space-separated.
CREATE TABLE IF NOT EXISTS deal_products (
    deal_id  TEXT PRIMARY KEY REFERENCES deals (id) ON DELETE CASCADE,
    models   TEXT NOT NULL,
    currency TEXT NOT NULL,
    cents    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS deal_products_models ON deal_products (models, currency);
