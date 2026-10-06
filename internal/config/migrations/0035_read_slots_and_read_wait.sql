-- 0035: a friend's read slots and read wait (internal/config/kind.go: Kinds,
-- "friend"; docs/SPEC-SPRINT.md section 6). read_slots is how many reads her
-- reader, reader-<friend>, runs at once, apart from her width (the jobs she
-- works): 2 by default, NULL is the default too, 0 asks her none, never
-- negative. read_wait is the seconds a read asked of her reader may wait not
-- begun before the tick raises the readers-behind judgment on her reader: 600
-- (ten minutes) by default, NULL is the default too, never below 1. Every row
-- there before this file takes the defaults. The file run again skips each
-- clause, so it never overwrites a value set since.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS read_slots integer DEFAULT 2
    CHECK (read_slots IS NULL OR read_slots >= 0);
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS read_wait integer DEFAULT 600
    CHECK (read_wait IS NULL OR read_wait >= 1);
