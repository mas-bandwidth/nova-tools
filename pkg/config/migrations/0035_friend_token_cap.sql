-- 0035: the friend's token_cap (pkg/config/kind.go: Kinds, "friend";
-- docs/SPEC-FRIEND.md, friend-token-cap-bb.w2): tokens one card may spend
-- before a one-shot lane stops its own run and holds the card. 6000000 by
-- default, the stopgap's cap; 0 is no cap. The file run again skips the clause.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS token_cap integer NOT NULL DEFAULT 6000000 CHECK (token_cap >= 0);
