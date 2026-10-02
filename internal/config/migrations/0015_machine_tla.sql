-- 0015: the machine's tla fact (internal/config/kind.go: Kinds, "machine"):
-- true marks a TLC record machine, a Linux bench the tools play installs the
-- pinned TLC jar on (fleet/tools.yml, the tla play; the inventory's tla
-- group) and tlacheck run --bench reads (tla/README.md, "The record
-- machines"). false, the default, is none, so every row there before this
-- file is none until someone sets it.
ALTER TABLE config.machines ADD COLUMN IF NOT EXISTS
    tla boolean NOT NULL DEFAULT false;
