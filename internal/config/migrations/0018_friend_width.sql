-- 0018: the friend's width (internal/config/kind.go: Kinds, "friend"), the
-- jobs she works at once, which nova-sprint friend sync sets on her row of
-- the friends table. Until now it was a constant 1 in nova-sprint (the owner,
-- 2026-10-02: "6/1 seems a bit wrong -- need to setup width for friends?
-- Start at 8 for each?", and "please update friends in nova-config so each
-- friend has a width of 8"), so every friend row there before this file is
-- set to 8 explicitly, and a row added later takes 8 by default. The fill
-- runs only when this file adds the column, so running the file again never
-- overwrites a width set since. A width is at least 1: a friend who works no
-- job at once is a row to remove, not a width. A row's history
-- (config.history) is not written by a migration, as 0013's was not.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                  WHERE table_schema = 'config' AND table_name = 'friends' AND column_name = 'width') THEN
    ALTER TABLE config.friends ADD COLUMN width integer NOT NULL DEFAULT 8 CHECK (width >= 1);
    UPDATE config.friends SET width = 8;
  END IF;
END $$;
