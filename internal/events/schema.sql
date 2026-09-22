-- schema.sql is the fold: what `ev:cards` becomes once it is a record rather than a queue.
-- It is applied on every open, statement by statement in one transaction, and is idempotent:
-- every table, index and view is IF NOT EXISTS and the version row is ON CONFLICT DO NOTHING.
--
-- THE EVENT ID IS THE PRIMARY KEY of all three tables. Streams deliver at least once, so a
-- redelivered entry must be a no-op rather than a second row: that single fact is what makes
-- "a hundred DONEs, kill the fold, restart, the count is a hundred" (Johnny's bar 3) a
-- property of the schema. It is also why a replay of the whole stream into a fresh file
-- produces the same rows as an incremental fold.
--
-- NO ROW CARRIES A FOLD TIMESTAMP, on purpose. `at` is the writer's stamp and travels with
-- the entry; a `folded_at` would make two folds of the same stream differ and there would be
-- nothing left to compare a rebuild against.

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);

-- attempts is the card's own life: queued, leased, started, turn, ok, fail, asked,
-- harvested, pr, jev. One row per event, not per card: the card is the `label` column and
-- the views do the counting.
CREATE TABLE IF NOT EXISTS attempts (
    event_id   TEXT PRIMARY KEY,
    label      TEXT    NOT NULL,
    attempt    INTEGER NOT NULL DEFAULT 0,
    bench      TEXT    NOT NULL DEFAULT '',
    model      TEXT    NOT NULL DEFAULT '',
    route      TEXT    NOT NULL DEFAULT '',
    kind       TEXT    NOT NULL,
    tokens_in  INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    usd        REAL    NOT NULL DEFAULT 0,
    pr         TEXT    NOT NULL DEFAULT '',
    head       TEXT    NOT NULL DEFAULT '',
    at         TEXT    NOT NULL DEFAULT '',
    day        TEXT    NOT NULL DEFAULT ''
);

-- reads is a friend's read of a pull request: the `read` kind, kept apart because the read
-- queue's numbers are not the card's and a join on label is how they meet.
CREATE TABLE IF NOT EXISTS reads (
    event_id   TEXT PRIMARY KEY,
    label      TEXT    NOT NULL,
    attempt    INTEGER NOT NULL DEFAULT 0,
    bench      TEXT    NOT NULL DEFAULT '',
    model      TEXT    NOT NULL DEFAULT '',
    route      TEXT    NOT NULL DEFAULT '',
    kind       TEXT    NOT NULL,
    tokens_in  INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    usd        REAL    NOT NULL DEFAULT 0,
    pr         TEXT    NOT NULL DEFAULT '',
    head       TEXT    NOT NULL DEFAULT '',
    at         TEXT    NOT NULL DEFAULT '',
    day        TEXT    NOT NULL DEFAULT ''
);

-- landings is the lander's merge, the only kind that proves a card was USEFUL. GitHub is
-- still the proof of DONE (Johnny); this table is the count beside it.
CREATE TABLE IF NOT EXISTS landings (
    event_id   TEXT PRIMARY KEY,
    label      TEXT    NOT NULL,
    attempt    INTEGER NOT NULL DEFAULT 0,
    bench      TEXT    NOT NULL DEFAULT '',
    model      TEXT    NOT NULL DEFAULT '',
    route      TEXT    NOT NULL DEFAULT '',
    kind       TEXT    NOT NULL,
    tokens_in  INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    usd        REAL    NOT NULL DEFAULT 0,
    pr         TEXT    NOT NULL DEFAULT '',
    head       TEXT    NOT NULL DEFAULT '',
    at         TEXT    NOT NULL DEFAULT '',
    day        TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS attempts_label_idx ON attempts (label);
CREATE INDEX IF NOT EXISTS attempts_kind_idx  ON attempts (kind);
CREATE INDEX IF NOT EXISTS attempts_day_idx   ON attempts (day);
CREATE INDEX IF NOT EXISTS attempts_route_idx ON attempts (model, route);
CREATE INDEX IF NOT EXISTS attempts_bench_idx ON attempts (bench);
CREATE INDEX IF NOT EXISTS reads_label_idx    ON reads (label);
CREATE INDEX IF NOT EXISTS landings_label_idx ON landings (label);

-- card_model_route is which model on which route ran a card: the first non-empty pair the
-- card's own events carry, by event id, so it is one deterministic answer per label. The
-- lander does not know the model, so a landing finds its model through this view.
CREATE VIEW IF NOT EXISTS card_model_route AS
SELECT a.label AS label,
       (SELECT m.model FROM attempts m WHERE m.label = a.label AND m.model <> '' ORDER BY m.event_id LIMIT 1) AS model,
       (SELECT r.route FROM attempts r WHERE r.label = a.label AND r.route <> '' ORDER BY r.event_id LIMIT 1) AS route
  FROM attempts a
 GROUP BY a.label;

CREATE VIEW IF NOT EXISTS landed_by_model_route AS
SELECT COALESCE(c.model, '') AS model,
       COALESCE(c.route, '') AS route,
       count(*)              AS landed
  FROM landings l
  LEFT JOIN card_model_route c ON c.label = l.label
 GROUP BY COALESCE(c.model, ''), COALESCE(c.route, '');

-- by_model_route is the row Glenn asked for: cost per USEFUL card, per route, beside cost
-- per OK card, so a dearer model that lands beats a cheap one that does not.
CREATE VIEW IF NOT EXISTS by_model_route AS
SELECT a.model                                                AS model,
       a.route                                                AS route,
       count(*)                                               AS "rows",
       sum(CASE WHEN a.kind = 'ok' THEN 1 ELSE 0 END)         AS ok,
       sum(CASE WHEN a.kind = 'fail' THEN 1 ELSE 0 END)       AS fail,
       sum(CASE WHEN a.kind IN ('ok','fail') THEN 1 ELSE 0 END) AS done,
       sum(a.tokens_in)                                       AS tokens_in,
       sum(a.tokens_out)                                      AS tokens_out,
       round(sum(a.usd), 6)                                   AS usd,
       CASE WHEN sum(CASE WHEN a.kind = 'ok' THEN 1 ELSE 0 END) > 0
            THEN round(sum(a.usd) / sum(CASE WHEN a.kind = 'ok' THEN 1 ELSE 0 END), 6) END AS usd_per_ok,
       COALESCE(l.landed, 0)                                  AS landed,
       CASE WHEN COALESCE(l.landed, 0) > 0
            THEN round(sum(a.usd) / l.landed, 6) END          AS usd_per_landed
  FROM attempts a
  LEFT JOIN landed_by_model_route l ON l.model = a.model AND l.route = a.route
 GROUP BY a.model, a.route;

CREATE VIEW IF NOT EXISTS by_bench AS
SELECT bench                                                  AS bench,
       count(*)                                               AS "rows",
       count(DISTINCT label)                                  AS cards,
       sum(CASE WHEN kind = 'ok' THEN 1 ELSE 0 END)           AS ok,
       sum(CASE WHEN kind = 'fail' THEN 1 ELSE 0 END)         AS fail,
       sum(CASE WHEN kind IN ('ok','fail') THEN 1 ELSE 0 END) AS done,
       round(sum(usd), 6)                                     AS usd
  FROM attempts
 GROUP BY bench;

CREATE VIEW IF NOT EXISTS by_day AS
SELECT day                                                    AS day,
       count(*)                                               AS "rows",
       count(DISTINCT label)                                  AS cards,
       sum(CASE WHEN kind = 'ok' THEN 1 ELSE 0 END)           AS ok,
       sum(CASE WHEN kind = 'fail' THEN 1 ELSE 0 END)         AS fail,
       sum(CASE WHEN kind IN ('ok','fail') THEN 1 ELSE 0 END) AS done,
       round(sum(usd), 6)                                     AS usd
  FROM attempts
 GROUP BY day;

-- by_label is one row per card: what `nova-pulse status` and the sprint table read instead
-- of counting files with `find`.
CREATE VIEW IF NOT EXISTS by_label AS
SELECT a.label                                                  AS label,
       count(*)                                                 AS "rows",
       max(a.attempt)                                           AS attempts,
       sum(CASE WHEN a.kind = 'ok' THEN 1 ELSE 0 END)           AS ok,
       sum(CASE WHEN a.kind = 'fail' THEN 1 ELSE 0 END)         AS fail,
       sum(CASE WHEN a.kind IN ('ok','fail') THEN 1 ELSE 0 END) AS done,
       round(sum(a.usd), 6)                                     AS usd,
       (SELECT count(*) FROM reads r WHERE r.label = a.label)    AS reads,
       (SELECT count(*) FROM landings l WHERE l.label = a.label) AS landed
  FROM attempts a
 GROUP BY a.label;

-- totals is the sprint table's own row: done, ok and fail from the fold, not from mtimes.
CREATE VIEW IF NOT EXISTS totals AS
SELECT (SELECT count(DISTINCT label) FROM attempts)                                  AS cards,
       (SELECT count(*) FROM attempts)                                               AS "rows",
       (SELECT count(*) FROM attempts WHERE kind IN ('ok','fail'))                   AS done,
       (SELECT count(*) FROM attempts WHERE kind = 'ok')                             AS ok,
       (SELECT count(*) FROM attempts WHERE kind = 'fail')                           AS fail,
       (SELECT count(*) FROM reads)                                                  AS reads,
       (SELECT count(*) FROM landings)                                               AS landed,
       (SELECT round(COALESCE(sum(usd), 0), 6) FROM attempts)                        AS usd;

INSERT INTO schema_version (version) VALUES (1) ON CONFLICT (version) DO NOTHING;
