-- 0003: the friend kind (internal/config/kind.go: Kinds, "friend"). machine
-- is a foreign key: a friend's desired slots are guarded by its machine's
-- ceiling, so a friend on no machine is refused by the structure.
CREATE TABLE IF NOT EXISTS config.friends (
    name       text PRIMARY KEY,
    machine    text NOT NULL REFERENCES config.machines (name),
    slots      integer NOT NULL DEFAULT 0 CHECK (slots >= 0),
    harness    text NOT NULL DEFAULT '',
    wake       text NOT NULL DEFAULT '',
    roles      text NOT NULL DEFAULT '',
    logins     text NOT NULL DEFAULT '',
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
