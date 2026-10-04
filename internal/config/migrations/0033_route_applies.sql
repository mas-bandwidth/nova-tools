-- 0033: the route's applies mask (internal/config/kind.go: Kinds, "route";
-- docs/SPEC-CONFIG.md, route). A comma list of the executor classes a route is
-- applied to (friends, fleet, local), or all (the default, every class): the
-- deal draws a work card only from the routes of its tier whose mask holds the
-- executor it is dealt to, and a read only on a reader whose class its route's
-- mask holds. The column is NOT NULL DEFAULT 'all', so every row before it is
-- used everywhere as it was.
ALTER TABLE config.routes
    ADD COLUMN IF NOT EXISTS applies text NOT NULL DEFAULT 'all';

-- The seed: every pro-* route runs on friends, every flash-* route on the
-- fleet, and every other route keeps all. Restricted to a row that still holds
-- all, so a second run changes nothing and a mask set since is never clobbered.
UPDATE config.routes SET applies = 'friends'
 WHERE name LIKE 'pro-%' AND applies = 'all';
UPDATE config.routes SET applies = 'fleet'
 WHERE name LIKE 'flash-%' AND applies = 'all';

-- A pro-* route disabled to keep pro work off the fleet (the owner, 2026-10-04,
-- 5:38 PM: the note names flash only to the fleet, or the owner's 5:38 PM
-- decision) is enabled again: the mask is now the way, so pro runs on friends
-- only. A pro route disabled for another measured reason (its ok rate, a
-- provider failure) keeps its `enabled=false` and its note.
UPDATE config.routes
   SET enabled = true,
       note = 'applies friends: pro routes run on friends only (the owner, 2026-10-04)'
 WHERE name LIKE 'pro-%'
   AND enabled = false
   AND (lower(note) LIKE '%flash only to the fleet%' OR note LIKE '%5:38%');
