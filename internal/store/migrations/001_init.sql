CREATE TABLE deals (
    id          TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    url         TEXT NOT NULL,
    title       TEXT NOT NULL,
    title_clean TEXT NOT NULL,
    merchant    TEXT NOT NULL DEFAULT '',
    price       TEXT NOT NULL DEFAULT '',
    category    TEXT NOT NULL DEFAULT '',
    image_url   TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL,
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL,
    in_trending INTEGER NOT NULL DEFAULT 0,
    in_new      INTEGER NOT NULL DEFAULT 0,
    temperature INTEGER
);
CREATE INDEX deals_fingerprint ON deals (fingerprint, first_seen);
CREATE INDEX deals_last_seen ON deals (last_seen);

CREATE TABLE temperature_snapshots (
    deal_id     TEXT NOT NULL REFERENCES deals (id) ON DELETE CASCADE,
    at          INTEGER NOT NULL,
    temperature INTEGER NOT NULL
);
CREATE INDEX temperature_snapshots_deal ON temperature_snapshots (deal_id, at);

CREATE TABLE verdicts (
    deal_id       TEXT PRIMARY KEY REFERENCES deals (id) ON DELETE CASCADE,
    decision      TEXT NOT NULL,
    score         INTEGER NOT NULL,
    watchlist_hit INTEGER NOT NULL,
    reasons       TEXT NOT NULL,
    decided_at    INTEGER NOT NULL
);

CREATE TABLE actions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    value      TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE notifications_sent (
    deal_id  TEXT NOT NULL REFERENCES deals (id) ON DELETE CASCADE,
    channel  TEXT NOT NULL,
    sent_at  INTEGER,
    attempts INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (deal_id, channel)
);
