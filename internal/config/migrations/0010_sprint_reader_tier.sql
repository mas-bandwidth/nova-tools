-- 0010: the sprint row's reader_tier (internal/config/kind.go: Kinds, "sprint"),
-- the model tier a read card's route is drawn from, at the tier's rolling index
-- as a work card's is (internal/sprint/route.go, readRouteOf); pro unless set.
-- Apply writes it to sprint:reader_tier, which the sprint's routes read takes.
ALTER TABLE config.sprint ADD COLUMN IF NOT EXISTS
    reader_tier text NOT NULL DEFAULT 'pro' CHECK (reader_tier IN ('flash', 'pro'));
