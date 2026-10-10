-- 0038: how a friend's work is paid (internal/config/kind.go, Kinds, "friend",
-- FriendBilling; the owner, 2026-10-04 4:41 PM). subscription (the default) is
-- tokens only, the friends category, never in the sprint's dollar columns; api
-- is work at API rates ("They are always API rate"), priced in dollars under its
-- model's tier as a fleet route's work is, and the deal offers a heavy or pro
-- card to a subscription friend before an api friend or a route takes it. Every
-- row before this file takes the default, and a friend at API rates is set by the
-- owner's word: nova-config friend set <friend> --billing api. The file run again
-- skips the clause, so it never overwrites a billing set since.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS billing text NOT NULL DEFAULT 'subscription' CHECK (billing IN ('api', 'subscription'));
