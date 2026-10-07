-- 0017: a loop carries no width of its own (internal/config/kind.go: Kinds,
-- "loop", and memberArgvSpellsWidth). A nova-swarm member's width, and a
-- reader's too, is its machine row's (machine set <m> --width <n>), moved to
-- the fleet row by nova-sprint fleet sync and read by the worker with its
-- queue every tick: a member its own row's, a reader its machine's, the one
-- reader on machine m being named reader-<m> (the owner, 2026-10-02: "The
-- reader widths seem to be very ad-hoc, unlike the machine widths"; "why not
-- just have as many readers as workers per-machine"). Until now a reader's
-- width was its loop row's: the width column 0013 filled from the argv, and
-- the argv's own --width before that, set by hand per reader and cut by hand
-- under load; and a machine ran two reader identities, reader-<m> and
-- reader-<m>-2, a workaround for reads returned with no verdict holding a
-- reader's lanes.
--
-- So, in order: the second reader rows (reader-<m>-2) are removed, one reader
-- per machine; every nova-swarm member argv, under env too, that spells a width (--width n,
-- -width n, --width=n, -width=n, before any --) has it taken out, the rest of
-- the argv kept word for word in its canonical compact JSON, another
-- program's --width left as its own; and the loops' width column goes. The
-- width each reader ran at is not carried anywhere: from this version it is
-- the machine's, as the member's is. A row's history (config.history) is not
-- written by a migration, as 0013's was not.
-- a second reader is the row whose name is its machine's name plus the -2
-- suffix, never a machine whose own name ends in -2: reader-bench-2 (machine
-- bench-2) is that machine's one reader, while reader-m1-2 (machine m1) is the
-- second reader of m1. Match the row's machine identity, not the name alone.
DELETE FROM config.loops WHERE name = 'reader-' || machine || '-2';

-- A member argv is nova-swarm member ..., or the same under env with its
-- NAME=value words first (the fleet's member rows: /usr/bin/env
-- NOVA_SPRINT_REDIS_USER=... nova-swarm member ...; kind.go: memberAt): m.k is
-- the position of its member word, m.stop that of the first -- after it.
UPDATE config.loops AS l
   SET argv = (
     SELECT '[' || string_agg(to_jsonb(e.t)::text, ',' ORDER BY e.i) || ']'
       FROM jsonb_array_elements_text(l.argv::jsonb) WITH ORDINALITY AS e(t, i)
       LEFT JOIN jsonb_array_elements_text(l.argv::jsonb) WITH ORDINALITY AS p(t, i) ON p.i = e.i - 1
      WHERE NOT (e.i > m.k
                 AND e.i < m.stop
                 AND (e.t IN ('--width', '-width') OR e.t ~ '^--?width=' OR p.t IN ('--width', '-width')))
   )
  FROM (SELECT x.name, x.q + 1 AS k,
               COALESCE((SELECT min(d.i) FROM jsonb_array_elements_text(x.argv::jsonb) WITH ORDINALITY AS d(t, i)
                          WHERE d.t = '--' AND d.i > x.q + 1), 2147483647) AS stop
          FROM (SELECT name, argv,
                       CASE WHEN argv::jsonb ->> 0 ~ '(^|/)env$'
                            THEN (SELECT min(w.i) FROM jsonb_array_elements_text(argv::jsonb) WITH ORDINALITY AS w(t, i)
                                   WHERE w.i > 1 AND strpos(w.t, '=') = 0)
                            ELSE 1 END AS q
                  FROM config.loops) AS x
         WHERE x.q IS NOT NULL
           AND x.argv::jsonb ->> (x.q::int - 1) ~ '(^|/)nova-swarm$'
           AND x.argv::jsonb ->> x.q::int = 'member') AS m
 WHERE m.name = l.name
   AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(l.argv::jsonb) WITH ORDINALITY AS w(t, i)
                WHERE w.i > m.k
                  AND w.i < m.stop
                  AND (w.t IN ('--width', '-width') OR w.t ~ '^--?width='));

ALTER TABLE config.loops DROP COLUMN IF EXISTS width;
