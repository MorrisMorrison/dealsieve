-- When each channel's last successful digest window ended (unix seconds).
CREATE TABLE IF NOT EXISTS digest_state (
    channel     TEXT PRIMARY KEY,
    last_cutoff INTEGER NOT NULL
);

-- Deals already listed in a channel's digest; each deal is listed once per channel.
CREATE TABLE IF NOT EXISTS digest_sent (
    channel  TEXT NOT NULL,
    deal_id  TEXT NOT NULL REFERENCES deals (id) ON DELETE CASCADE,
    sent_at  INTEGER NOT NULL,
    PRIMARY KEY (channel, deal_id)
);
