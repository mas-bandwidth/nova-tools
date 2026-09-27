-- 0003: the friend kind (internal/config/kind.go: Kinds, "friend"). The row
-- is what someone decides for a friend: how wide (slots), which tiers she
-- can do, and her roles. Where she runs, her harness and her logins are
-- what she would just know, runtime data her own presence reports, never
-- columns here; who coordinates is the sprint row's field (0005).
CREATE TABLE IF NOT EXISTS config.friends (
    name       text PRIMARY KEY,
    slots      integer NOT NULL DEFAULT 0 CHECK (slots >= 0),
    tiers      text NOT NULL DEFAULT '',
    roles      text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
