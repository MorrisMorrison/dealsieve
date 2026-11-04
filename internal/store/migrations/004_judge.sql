-- One LLM opinion per deal; 'failed' rows are retried a few times.
CREATE TABLE IF NOT EXISTS opinions (
    deal_id   TEXT PRIMARY KEY REFERENCES deals (id) ON DELETE CASCADE,
    status    TEXT NOT NULL,
    rating    TEXT NOT NULL DEFAULT '',
    reason    TEXT NOT NULL DEFAULT '',
    model     TEXT NOT NULL DEFAULT '',
    error     TEXT NOT NULL DEFAULT '',
    attempts  INTEGER NOT NULL,
    judged_at INTEGER NOT NULL
);

-- When each judge call was made (unix seconds), for the daily limit.
CREATE TABLE IF NOT EXISTS judge_calls (
    at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS judge_calls_at ON judge_calls (at);
