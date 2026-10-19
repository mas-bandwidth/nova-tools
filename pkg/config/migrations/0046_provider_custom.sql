-- 0046: a provider the pinned harness's catalog does not know (paperinstruments) is
-- declared direct in the job's opencode.json from its row: the API base, the NAME of the
-- environment variable holding the key, and the npm adapter. Empty (the default) means the
-- provider is catalogued and needs no block. The key is never stored here, only its name.
ALTER TABLE config.providers
    ADD COLUMN IF NOT EXISTS base_url text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS key_env text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS npm_adapter text NOT NULL DEFAULT '';
