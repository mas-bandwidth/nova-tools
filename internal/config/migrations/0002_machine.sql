-- 0002: the machine kind (internal/config/kind.go: Kinds, "machine"). The
-- row is exactly the declared facts something reads: the name is the tailnet
-- host (ssh <name> reaches it; there is no address column), user is the
-- login the plays and seals use, seat the nova-secrets seat, slots how many
-- cards it may run, runners how many CI runners it hosts. Measured facts
-- (os, arch, cores, memory) are never columns: they come live from the
-- machine's beat (docs/SPEC-CONFIG.md, "Declared and measured").
CREATE TABLE IF NOT EXISTS config.machines (
    name       text PRIMARY KEY,
    "user"     text NOT NULL,
    seat       text NOT NULL,
    slots      integer NOT NULL DEFAULT 0 CHECK (slots >= 0),
    runners    integer NOT NULL DEFAULT 0 CHECK (runners >= 0),
    tiers      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
