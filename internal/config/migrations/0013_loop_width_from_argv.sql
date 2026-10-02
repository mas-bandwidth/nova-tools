-- 0013: a loop's width field becomes the width its command runs with
-- (internal/config/kind.go: LoopCommand). From this version a width above 0
-- is the value of the argv's --width when the unit is rendered; until it, the
-- field was carried and nothing rendered it, and the argv's own --width was
-- what ran. So each row's field is set to the value its argv's --width
-- carries now (the last of --width n, -width n, --width=n, -width=n after the
-- program word and before a --, when that value is a whole number), and to 0
-- when the argv carries none: 0 leaves the argv as written. Every loop's
-- command after this migration is the command it ran before it.
UPDATE config.loops AS l
   SET width = COALESCE((
         SELECT CASE
                  WHEN e.t IN ('--width', '-width') AND n.t ~ '^[0-9]{1,9}$' THEN n.t::integer
                  WHEN e.t ~ '^--?width=[0-9]{1,9}$' THEN substring(e.t FROM '=([0-9]+)$')::integer
                END
           FROM jsonb_array_elements_text(l.argv::jsonb) WITH ORDINALITY AS e(t, i)
           LEFT JOIN jsonb_array_elements_text(l.argv::jsonb) WITH ORDINALITY AS n(t, i) ON n.i = e.i + 1
          WHERE e.i > 1
            AND (e.t IN ('--width', '-width') OR e.t ~ '^--?width=')
            AND e.i < COALESCE((SELECT min(d.i) FROM jsonb_array_elements_text(l.argv::jsonb) WITH ORDINALITY AS d(t, i)
                                 WHERE d.t = '--' AND d.i > 1), 2147483647)
          ORDER BY e.i DESC
          LIMIT 1), 0);
