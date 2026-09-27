-- 0002: the machine kind (internal/config/kind.go: Kinds, "machine"). The
-- fleet registry's row: what a machine is, so what may be placed on it.
CREATE TABLE IF NOT EXISTS config.machines (
    name       text PRIMARY KEY,
    ssh        text NOT NULL,
    os_arch    text NOT NULL,
    slots      integer NOT NULL DEFAULT 0 CHECK (slots >= 0),
    cores      integer NOT NULL DEFAULT 0 CHECK (cores >= 0),
    roles      text NOT NULL DEFAULT '',
    seat       text NOT NULL DEFAULT '',
    "user"     text NOT NULL DEFAULT '',
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
