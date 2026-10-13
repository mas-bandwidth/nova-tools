-- 0044: a friend's pronouns may be unstated. 0038 added the column
-- NOT NULL DEFAULT 'they/them'. A stored they/them stays stated. This file
-- drops NOT NULL and the default and rewrites no row. NULL is unstated and
-- reads as they/them. 0042 and 0043 are reserved and absent. Run again is
-- the same two drops.
ALTER TABLE config.friends ALTER COLUMN pronouns DROP NOT NULL;
ALTER TABLE config.friends ALTER COLUMN pronouns DROP DEFAULT;
