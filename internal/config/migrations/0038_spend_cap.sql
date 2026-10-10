-- 0038: the hourly dollar cap (internal/config/kind.go: Kinds, "route" and "friend";
-- docs/SPEC-CONFIG.md, spend-circuit-breakerb-bb.w8). A route past cap_usd_hour of
-- cost in a clock hour rests to the next hour; a friend past hers is dealt no
-- card until it. A decimal kept as text in its one spelling, never a float, 0 for no
-- cap, '' taking the kind's default. A route's is '' when it takes its tier's
-- (flash 5, pro 20, heavy 0); the rows that exist are set to theirs here. A friend's
-- is 10 unless her row says otherwise. The file run again changes no row: only a
-- route still at '' is set.
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS cap_usd_hour text NOT NULL DEFAULT '' CHECK (
        cap_usd_hour = '' OR cap_usd_hour ~ '^[0-9]+(\.[0-9]+)?$');
UPDATE config.routes
   SET cap_usd_hour = CASE tier WHEN 'flash' THEN '5' WHEN 'pro' THEN '20' ELSE '0' END
 WHERE cap_usd_hour = '';
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS cap_usd_hour text NOT NULL DEFAULT '10' CHECK (
        cap_usd_hour = '' OR cap_usd_hour ~ '^[0-9]+(\.[0-9]+)?$');
