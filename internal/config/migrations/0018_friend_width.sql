-- 0018: the friend's width (internal/config/kind.go: Kinds, "friend"), the
-- jobs she works at once, which nova-sprint friend sync sets on her row of
-- the friends table. Until now it was a constant 1 in nova-sprint (the owner,
-- 2026-10-02: "6/1 seems a bit wrong -- need to setup width for friends?
-- Start at 8 for each?", and "please update friends in nova-config so each
-- friend has a width of 8"). The fixed default sets every friend row there
-- before this file to 8, and a row added later takes 8; the file run again
-- skips the whole clause, so it never overwrites a width set since. A width
-- is at least 1: a friend who works no job at once is a row to remove, not a
-- width.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS width integer NOT NULL DEFAULT 8 CHECK (width >= 1);
