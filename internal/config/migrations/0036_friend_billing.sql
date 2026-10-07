-- 0036: how a friend's work is paid (internal/config/kind.go: BillingAPI,
-- BillingSubscription, FriendBilling; docs/SPEC-CONFIG.md, friend). subscription
-- (the default) is tokens only, the friends category, never in the dollar
-- columns; api is work at API rates, priced in dollars under its model's tier as
-- a fleet route's work is. Every row there before this file takes the default,
-- and the owner sets an API friend's word with nova-config friend set <friend>
-- --billing api. The file run again skips the clause, so it never overwrites a
-- billing set since.
ALTER TABLE config.friends
    ADD COLUMN IF NOT EXISTS billing text NOT NULL DEFAULT 'subscription' CHECK (billing IN ('api', 'subscription'));
