-- schema.sql is the fold: what `cards:done` becomes once it is a record rather than a queue.
-- It is a FOLD of that one stream, not a second source of truth beside it: the stream is the
-- record, and `nova-pulse fold --rebuild` recomputes this file from it at any moment.
-- It is applied on every open, statement by statement in one transaction (BEGIN IMMEDIATE ...
-- COMMIT), and is idempotent: every table and index is IF NOT EXISTS, every view is dropped
-- and recreated (so no file keeps an older view definition), and the version rows are
-- ON CONFLICT DO NOTHING. No statement touches a table's rows.
--
-- THE EVENT ID IS THE PRIMARY KEY of every table (the three card tables and decisions). Streams deliver at least once, so a
-- redelivered entry must be a no-op rather than a second row: that single fact is what makes
-- "a hundred DONEs, kill the fold, restart, the count is a hundred" (Johnny's bar 3) a
-- property of the schema. It is also why a replay of the whole stream into a fresh file
-- produces the same rows as an incremental fold.
--
-- NO ROW CARRIES A FOLD TIMESTAMP, on purpose. `at` is the writer's stamp and travels with
-- the entry; a `folded_at` would make two folds of the same stream differ and there would be
-- nothing left to compare a rebuild against.

-- tokens_in, tokens_out and usd are NULLABLE with no default, on purpose: an entry that
-- carries no cost has not reported a zero cost (no evidence is not negative evidence). SUM
-- skips a NULL, and a sum over nothing but NULLs is NULL, which the report prints as a dash.

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
    tokens_in  INTEGER,
    tokens_out INTEGER,
    usd        REAL,
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
    tokens_in  INTEGER,
    tokens_out INTEGER,
    usd        REAL,
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
    tokens_in  INTEGER,
    tokens_out INTEGER,
    usd        REAL,
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

-- Version 3 (nova-tools #3159): every view is rebuilt on open. The views are dropped here,
-- dependents first, and recreated below in the file's order, so a file opened under an older
-- schema carries this file's definitions after one open. decisions_by_kind is dropped and
-- recreated unchanged, so the one rule holds for every view.
DROP VIEW IF EXISTS by_model_route;
DROP VIEW IF EXISTS landed_by_model_route;
DROP VIEW IF EXISTS card_model_route;
DROP VIEW IF EXISTS by_bench;
DROP VIEW IF EXISTS by_day;
DROP VIEW IF EXISTS by_label;
DROP VIEW IF EXISTS totals;
DROP VIEW IF EXISTS decisions_by_kind;

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
--
-- usd_per_landed is a lower bound unless every card in the group is priced (#3159): TEXT,
-- `>=<x> coverage=<p>% (<priced>/<cards>)` below 100% and `=<x> coverage=100.00% (<n>/<n>)`
-- at 100%, or NULL (the dash) when nothing landed or nothing is priced. A card is priced
-- when every attempt number it has carries at least one row with a non-NULL usd: a queued
-- or leased row never carries usd, so a per-row rule would price almost no card.
CREATE VIEW IF NOT EXISTS by_model_route AS
SELECT g.model, g.route, g."rows", g.ok, g.fail, g.done, g.tokens_in, g.tokens_out, g.usd, g.usd_per_ok,
       g.landed, g.cards, g.priced_cards,
       CASE WHEN g.landed > 0 AND g.usd_sum IS NOT NULL
            THEN printf('%s%.2f coverage=%.2f%% (%d/%d)',
                        CASE WHEN g.priced_cards = g.cards THEN '=' ELSE '>=' END,
                        g.usd_sum / g.landed, 100.0 * g.priced_cards / g.cards, g.priced_cards, g.cards) END AS usd_per_landed
  FROM (SELECT a.model                                                AS model,
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
               count(DISTINCT a.label)                                AS cards,
               count(DISTINCT CASE WHEN p.priced THEN a.label END)    AS priced_cards,
               sum(a.usd)                                             AS usd_sum
          FROM attempts a
          LEFT JOIN landed_by_model_route l ON l.model = a.model AND l.route = a.route
          LEFT JOIN (SELECT label,
                            count(DISTINCT attempt) = count(DISTINCT CASE WHEN usd IS NOT NULL THEN attempt END) AS priced
                       FROM attempts
                      GROUP BY label) p ON p.label = a.label
         GROUP BY a.model, a.route) g;

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
-- priced_cards and usd_per_landed follow by_model_route's rule (#3159), over every card.
CREATE VIEW IF NOT EXISTS totals AS
SELECT t.cards, t."rows", t.done, t.ok, t.fail, t.reads, t.landed, t.usd, t.priced_cards,
       CASE WHEN t.landed > 0 AND t.usd_sum IS NOT NULL
            THEN printf('%s%.2f coverage=%.2f%% (%d/%d)',
                        CASE WHEN t.priced_cards = t.cards THEN '=' ELSE '>=' END,
                        t.usd_sum / t.landed, 100.0 * t.priced_cards / t.cards, t.priced_cards, t.cards) END AS usd_per_landed
  FROM (SELECT (SELECT count(DISTINCT label) FROM attempts)                                  AS cards,
               (SELECT count(*) FROM attempts)                                               AS "rows",
               (SELECT count(*) FROM attempts WHERE kind IN ('ok','fail'))                   AS done,
               (SELECT count(*) FROM attempts WHERE kind = 'ok')                             AS ok,
               (SELECT count(*) FROM attempts WHERE kind = 'fail')                           AS fail,
               (SELECT count(*) FROM reads)                                                  AS reads,
               (SELECT count(*) FROM landings)                                               AS landed,
               (SELECT round(sum(usd), 6) FROM attempts)                                     AS usd,
               (SELECT sum(usd) FROM attempts)                                               AS usd_sum,
               (SELECT count(*) FROM (SELECT label FROM attempts GROUP BY label
                  HAVING count(DISTINCT attempt) = count(DISTINCT CASE WHEN usd IS NOT NULL THEN attempt END))) AS priced_cards) t;

INSERT INTO schema_version (version) VALUES (1) ON CONFLICT (version) DO NOTHING;

-- Version 2 (nova-tools #2623): the decision record. A Jev routing decision is one `decide`
-- entry on cards:done, and this table is where the fold keeps it: decide_log's columns under
-- decide_log's names, so the calibration set reads the same as it did in that table, which
-- is retired. `ts` is the entry's `at` and the BIGSERIAL id is the event id. `evidence`, a
-- JSON document, is not on the stream (ids and counts only); its measured columns are.
--
-- Every string the decision may not carry and every optional number is NULLABLE with no
-- default: an absent reason, refusal, outcome, confidence or token count is NULL, never ''
-- or 0 (no evidence is not negative evidence). Counts and flags the writer always sends are
-- NOT NULL. The table is IF NOT EXISTS, so a version-1 file gains it on its next open and a
-- `fold --rebuild` fills it from the stream.
CREATE TABLE IF NOT EXISTS decisions (
    event_id             TEXT PRIMARY KEY,
    label                TEXT    NOT NULL,
    bench                TEXT    NOT NULL DEFAULT '',
    model                TEXT    NOT NULL DEFAULT '',
    route                TEXT    NOT NULL DEFAULT '',
    pr                   TEXT    NOT NULL DEFAULT '',
    head                 TEXT    NOT NULL DEFAULT '',
    at                   TEXT    NOT NULL DEFAULT '',
    day                  TEXT    NOT NULL DEFAULT '',
    unit_id              TEXT    NOT NULL,
    kind                 TEXT,
    files                INTEGER NOT NULL DEFAULT 0,
    packages             INTEGER NOT NULL DEFAULT 0,
    lanes                INTEGER NOT NULL DEFAULT 0,
    lane                 TEXT,
    rung_tried           TEXT,
    height               INTEGER NOT NULL DEFAULT 0,
    confidence           REAL,
    floor                REAL,
    stepped_up           INTEGER NOT NULL DEFAULT 0,
    escalated            INTEGER NOT NULL DEFAULT 0,
    designated           INTEGER NOT NULL DEFAULT 0,
    source               TEXT,
    rowan_pick           TEXT,
    reason               TEXT,
    wait                 TEXT,
    awaiting_termination INTEGER NOT NULL DEFAULT 0,
    refusal              TEXT,
    outcome              TEXT,
    rung_succeeded       TEXT,
    calls                INTEGER NOT NULL DEFAULT 0,
    tokens_in            INTEGER,
    tokens_out           INTEGER,
    usd                  REAL,
    usage_failed         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS decisions_kind_idx ON decisions (kind);
CREATE INDEX IF NOT EXISTS decisions_unit_idx ON decisions (unit_id);
CREATE INDEX IF NOT EXISTS decisions_day_idx  ON decisions (day);

-- decisions_by_kind is what `nova-decide log --summary` used to be asked of the table: per
-- unit kind, how many decisions, how many stepped up or escalated or were refused, and what
-- they spent. A sum over nothing but NULL tokens is NULL, printed as a dash.
CREATE VIEW IF NOT EXISTS decisions_by_kind AS
SELECT COALESCE(kind, '')                                     AS kind,
       count(*)                                               AS decisions,
       count(DISTINCT unit_id)                                AS units,
       sum(stepped_up)                                        AS stepped_up,
       sum(escalated)                                         AS escalated,
       sum(CASE WHEN refusal IS NOT NULL THEN 1 ELSE 0 END)   AS refused,
       sum(calls)                                             AS calls,
       sum(tokens_in)                                         AS tokens_in,
       sum(tokens_out)                                        AS tokens_out
  FROM decisions
 GROUP BY COALESCE(kind, '');

INSERT INTO schema_version (version) VALUES (2) ON CONFLICT (version) DO NOTHING;

-- Version 3 (nova-tools #3159): the views above are rebuilt on every open, and totals and
-- by_model_route carry $/landed as a lower bound with its coverage. No table changed.
INSERT INTO schema_version (version) VALUES (3) ON CONFLICT (version) DO NOTHING;
