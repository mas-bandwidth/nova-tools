-- 0037: the machine's harnesses (internal/config/kind.go: Kinds, "machine";
-- docs/SPEC-CONFIG.md, machine): the harnesses the sprint's member on it can
-- launch, a comma list of opencode, claude, codex, grok. opencode alone is what
-- every machine launched before this file (the providers table, by a provider
-- key), so it is the default and every row there before it takes it; a
-- headless harness is listed only on a machine that holds its subscription
-- login (nova-config machine set <m> --harnesses claude,codex). The file run
-- again skips the clause, so it never overwrites a list set since.
ALTER TABLE config.machines
    ADD COLUMN IF NOT EXISTS harnesses text NOT NULL DEFAULT 'opencode';
