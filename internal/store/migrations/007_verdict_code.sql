-- Which rule blocked a deal (a rules.Code such as "hidden" or "junk"); NULL for
-- a pass and for verdicts saved before this column, until the next recompute.
ALTER TABLE verdicts ADD COLUMN block_code TEXT;
