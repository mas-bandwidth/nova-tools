-- 0047: a machine whose sprint member takes only the cards that name one of its gates
-- (pkg/config/kind.go: Kinds, "machine", gates_only): a comma list of GATE-NEEDS words such
-- as windows. Empty (the default) is a member dealt any card its facts satisfy. nova-sprint
-- fleet sync writes it onto the member's control card, where the deal reads it.
ALTER TABLE config.machines
    ADD COLUMN IF NOT EXISTS gates_only text NOT NULL DEFAULT '';
