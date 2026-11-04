CREATE TABLE IF NOT EXISTS settings_versions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    document   TEXT NOT NULL,
    summary    TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS dismissed_suggestions (
    kind       TEXT NOT NULL,
    value      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (kind, value)
);

-- Muting now lives in the settings lists.
DELETE FROM actions WHERE kind IN ('mute_category', 'mute_merchant');
