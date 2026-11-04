-- One row per click: keep the oldest of each (kind, value), then forbid repeats.
DELETE FROM actions WHERE id NOT IN (SELECT MIN(id) FROM actions GROUP BY kind, value);
CREATE UNIQUE INDEX IF NOT EXISTS actions_kind_value ON actions (kind, value);
