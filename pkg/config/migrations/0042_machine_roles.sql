-- 0042: machine inventory roles are explicit configuration. Existing config
-- inventory membership is reproducible from the current machine rows and
-- fleet references: every row is a bench, the referenced store is services,
-- and the referenced coordinator is coordination. Runner is never inferred
-- from the runners count. External machines.tsv roles are not available here.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'config' AND table_name = 'machines' AND column_name = 'roles'
    ) THEN
        ALTER TABLE config.machines ADD COLUMN roles text NOT NULL DEFAULT 'bench'
            CHECK (roles = '' OR roles ~ '^(bench|coordination|runner|services)(,(bench|coordination|runner|services))*$');
        UPDATE config.machines AS m
        SET roles = concat_ws(',', 'bench',
            CASE WHEN f.coordinator = m.name THEN 'coordination' END,
            CASE WHEN f.store = m.name THEN 'services' END)
        FROM config.fleet AS f
        WHERE f.name = 'fleet';
    END IF;
END $$;
