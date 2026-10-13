-- 0041: provider funding is declared configuration, never inferred from a route.
CREATE TABLE IF NOT EXISTS config.providers (
    name text PRIMARY KEY CONSTRAINT providers_name_pattern CHECK (name ~ '^[a-z0-9][a-z0-9-]*$'),
    billing text NOT NULL CHECK (billing IN ('metered', 'plan', 'both')),
    funded boolean NOT NULL DEFAULT false,
    balance_usd text NOT NULL DEFAULT '' CHECK (balance_usd = '' OR balance_usd ~ '^[0-9]+(\.[0-9]+)?$'),
    balance_as_of text NOT NULL DEFAULT '',
    console_url text NOT NULL DEFAULT '',
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (balance_usd = '' OR balance_as_of <> '')
);
INSERT INTO config.providers (name, billing, funded, note)
SELECT DISTINCT provider, 'metered', false, 'made by migrate from the routes: set --funded and --billing'
FROM config.routes
ON CONFLICT (name) DO NOTHING;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'config.routes'::regclass AND conname = 'routes_provider_fkey') THEN
        ALTER TABLE config.routes ADD CONSTRAINT routes_provider_fkey FOREIGN KEY (provider) REFERENCES config.providers(name);
    END IF;
END $$;
