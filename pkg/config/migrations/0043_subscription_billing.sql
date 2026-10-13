-- Preserve existing billing modes; subscription has zero marginal card cost.
ALTER TABLE config.routes DROP CONSTRAINT routes_billing_check;
ALTER TABLE config.routes ADD CONSTRAINT routes_billing_check CHECK (billing IN ('metered', 'plan', 'subscription'));
ALTER TABLE config.providers DROP CONSTRAINT providers_billing_check;
ALTER TABLE config.providers ADD CONSTRAINT providers_billing_check CHECK (billing IN ('metered', 'plan', 'both', 'subscription'));
