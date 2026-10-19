-- 0048: a provider row's max_output is the model's output limit, written as
-- models.<model>.limit.output in an uncatalogued provider's job config, so the harness asks
-- for no more than it. Empty (the default) means undeclared.
ALTER TABLE config.providers
    ADD COLUMN IF NOT EXISTS max_output integer;
