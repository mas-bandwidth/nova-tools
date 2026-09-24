-- three-card-priced.sql: three-card.sql with c3 priced at $1.00, so every card is priced and
-- the figure is exact: `=6.00 coverage=100.00% (3/3)` (nova-tools #3159).
INSERT INTO attempts (event_id, label, attempt, bench, model, route, kind, usd, at, day) VALUES
    ('1-0', 'c1', 1, 'studio', 'fable', 'studio', 'ok', 2.00, '2026-09-22T00:00:01Z', '2026-09-22'),
    ('2-0', 'c2', 1, 'studio', 'fable', 'studio', 'ok', 3.00, '2026-09-22T00:00:02Z', '2026-09-22'),
    ('3-0', 'c3', 1, 'studio', 'fable', 'studio', 'ok', 1.00, '2026-09-22T00:00:03Z', '2026-09-22');
INSERT INTO landings (event_id, label, kind, pr, at, day) VALUES
    ('4-0', 'c1', 'landed', '101', '2026-09-22T00:00:04Z', '2026-09-22');
