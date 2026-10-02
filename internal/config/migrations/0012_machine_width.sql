-- 0012: the machine's width (internal/config/kind.go: Kinds, "machine"), the
-- most work cards the sprint's member on the machine runs at once, set
-- directly; 0 is no member. Until now the width was derived: the machine's
-- slots less the slots of the friends charged to it, where a friend was
-- charged to the machine her live beat named. A migration cannot read beats,
-- so the fill charges nothing: width = slots for every existing row, and the
-- operator sets by hand any machine whose friends were charged to it. The
-- fill runs only when this file adds the column, so running the file again
-- never overwrites a width set since.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                 WHERE table_schema = 'config' AND table_name = 'machines' AND column_name = 'width') THEN
    ALTER TABLE config.machines ADD COLUMN width integer NOT NULL DEFAULT 0 CHECK (width >= 0);
    UPDATE config.machines SET width = slots;
  END IF;
END $$;
