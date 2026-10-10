-- 0012: the machine's width (pkg/config/kind.go: Kinds, "machine"), the
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
-- running the file again never overwrites a width set since. A width column
-- that is already there is checked to be the one this file makes (integer,
-- NOT NULL, default 0, CHECK width >= 0), and the migration fails naming
-- what differs rather than keeping a column the code does not expect.
DO $$
DECLARE
  col record;
  problems text := '';
BEGIN
  SELECT data_type, is_nullable, column_default INTO col
    FROM information_schema.columns
   WHERE table_schema = 'config' AND table_name = 'machines' AND column_name = 'width';
  IF NOT FOUND THEN
    ALTER TABLE config.machines ADD COLUMN width integer NOT NULL DEFAULT 0 CHECK (width >= 0);
    UPDATE config.machines AS m
       SET width = GREATEST(0, m.slots - CASE
             WHEN m.name = (SELECT coordinator FROM config.fleet WHERE name = 'fleet')
             THEN (SELECT COALESCE(SUM(slots), 0) FROM config.friends)
             ELSE 0 END);
    RETURN;
  END IF;
  IF col.data_type <> 'integer' THEN
    problems := problems || format(' its type is %s, want integer;', col.data_type);
  END IF;
  IF col.is_nullable <> 'NO' THEN
    problems := problems || ' it is nullable, want NOT NULL;';
  END IF;
  IF col.column_default IS DISTINCT FROM '0' THEN
    problems := problems || format(' its default is %s, want 0;', coalesce(col.column_default, 'none'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'config.machines'::regclass AND contype = 'c'
                    AND pg_get_constraintdef(oid) = 'CHECK ((width >= 0))') THEN
    problems := problems || ' it has no CHECK (width >= 0);';
  END IF;
  IF problems <> '' THEN
    RAISE EXCEPTION 'config.machines already has a width column that is not the one 0012 makes:% alter it to integer NOT NULL DEFAULT 0 CHECK (width >= 0), or drop it, then run nova-config migrate again', problems;
  END IF;
END $$;
