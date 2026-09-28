-- fold-2026-09-22.sql: the 2026-09-22 fold's cost shape, for TestFold20260922UsdPerLandedIsLowerBound
-- (nova-tools #3159). This file is its own generator: the recursive CTEs below ARE the fixture,
-- so there is no generator script beside it and nothing on a bench to run. Load it into a fold
-- file opened by OpenDB (the tables must exist); it is deterministic and inserts:
--
--   1902 cards, card-0001 .. card-1902, two attempt rows each at attempt 1: a `queued` row with
--        NULL usd (queued rows never carry usd) and an `ok` row;
--   10   cards (card-0001 .. card-0010) whose `ok` row has NULL usd: unpriced, so 1892 priced;
--   $619.03 in all: 1891 cards at $0.32 ($605.12) and card-0011 at $13.91;
--   11   landings, card-0011 .. card-0021.
--
-- So the old view printed usd_per_landed 56.275455 (619.03 / 11) as if exact, and the bound is
-- `>=56.28 coverage=99.47% (1892/1902)`. The per-row priced rule of spec rev 3 would count 0
-- priced cards here, because every card has a NULL-usd queued row.
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1902)
INSERT INTO attempts (event_id, label, attempt, bench, model, route, kind, usd, at, day)
SELECT printf('1758499200000-%d', 2 * i - 1),
       printf('card-%04d', i),
       1,
       CASE i % 2 WHEN 0 THEN 'studio' ELSE 'hulk' END,
       CASE i % 2 WHEN 0 THEN 'fable' ELSE 'sonnet' END,
       CASE i % 2 WHEN 0 THEN 'studio' ELSE 'hulk' END,
       'queued',
       NULL,
       '2026-09-22T00:00:00Z',
       '2026-09-22'
  FROM n;

WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1902)
INSERT INTO attempts (event_id, label, attempt, bench, model, route, kind, usd, at, day)
SELECT printf('1758499200000-%d', 2 * i),
       printf('card-%04d', i),
       1,
       CASE i % 2 WHEN 0 THEN 'studio' ELSE 'hulk' END,
       CASE i % 2 WHEN 0 THEN 'fable' ELSE 'sonnet' END,
       CASE i % 2 WHEN 0 THEN 'studio' ELSE 'hulk' END,
       'ok',
       CASE WHEN i <= 10 THEN NULL WHEN i = 11 THEN 13.91 ELSE 0.32 END,
       '2026-09-22T00:00:00Z',
       '2026-09-22'
  FROM n;

WITH RECURSIVE n(i) AS (SELECT 11 UNION ALL SELECT i + 1 FROM n WHERE i < 21)
INSERT INTO landings (event_id, label, kind, pr, at, day)
SELECT printf('1758499300000-%d', i),
       printf('card-%04d', i),
       'landed',
       printf('%d', 3000 + i),
       '2026-09-22T12:00:00Z',
       '2026-09-22'
  FROM n;
