-- 0033: a friend's read slots (internal/config/kind.go, friend), how many
-- reads her reader runs at once, apart from her width (the jobs she works).
-- 2 by default. NULL is the default too. 0 asks her none. Never negative.
-- Existing rows take 2. The file run again skips the clause.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS read_slots integer DEFAULT 2
    CHECK (read_slots IS NULL OR read_slots >= 0);
