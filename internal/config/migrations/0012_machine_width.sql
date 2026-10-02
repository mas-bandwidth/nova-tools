-- 0012: the machine's width (internal/config/kind.go: Kinds, "machine"), the
-- most work cards the sprint's member on the machine runs at once, set
-- directly; 0 is no member. Until now the width was derived: the machine's
-- slots less the slots of the friends charged to it, where a friend was
-- charged to the machine her live beat named, else to the fleet row's
-- coordinator machine. A migration reads no beats, so the fill applies the
-- beat-free part of that rule: every friend's slots are charged to the
-- coordinator machine (width = slots less their sum, never below 0), and
-- every other machine, and every machine when the fleet names no
-- coordinator, gets width = slots. That is the old width exactly when no
-- friend with slots had a beat naming a machine other than the coordinator;
-- for one that had, the fill charges her to the coordinator instead, and
-- the operator compares the filled widths with the fleet table fleet sync
-- last wrote before syncing again (docs/nova-config/README.md, "Migrating
-- to a set width"). The fill runs only when this file adds the column, so
-- running the file again never overwrites a width set since.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                 WHERE table_schema = 'config' AND table_name = 'machines' AND column_name = 'width') THEN
    ALTER TABLE config.machines ADD COLUMN width integer NOT NULL DEFAULT 0 CHECK (width >= 0);
    UPDATE config.machines AS m
       SET width = GREATEST(0, m.slots - CASE
             WHEN m.name = (SELECT coordinator FROM config.fleet WHERE name = 'fleet')
             THEN (SELECT COALESCE(SUM(slots), 0) FROM config.friends)
             ELSE 0 END);
  END IF;
END $$;
