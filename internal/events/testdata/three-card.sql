-- three-card.sql: the smallest fold with a bound (nova-tools #3159). Three cards on one
-- model and route, one ok row each: c1 $2.00, c2 $3.00, c3 unpriced (NULL usd); c1 landed.
-- $/landed is at least $5.00, from two of three cards: `>=5.00 coverage=66.67% (2/3)`.
-- The column list is the one every schema version has, so it loads under v1, v2 and v3.
INSERT INTO attempts (event_id, label, attempt, bench, model, route, kind, usd, at, day) VALUES
    ('1-0', 'c1', 1, 'studio', 'fable', 'studio', 'ok', 2.00, '2026-09-22T00:00:01Z', '2026-09-22'),
    ('2-0', 'c2', 1, 'studio', 'fable', 'studio', 'ok', 3.00, '2026-09-22T00:00:02Z', '2026-09-22'),
    ('3-0', 'c3', 1, 'studio', 'fable', 'studio', 'ok', NULL, '2026-09-22T00:00:03Z', '2026-09-22');
INSERT INTO landings (event_id, label, kind, pr, at, day) VALUES
    ('4-0', 'c1', 'landed', '101', '2026-09-22T00:00:04Z', '2026-09-22');
