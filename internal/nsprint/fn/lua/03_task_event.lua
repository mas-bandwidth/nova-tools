-- The events move the tasks (nova-tools#3779; rowan-new specs/ws-index.md,
-- "Tasks are cards", Glenn 2026-09-25 07:52 AM ET: "You should not have to
-- continually remember to maintain the correct structure, it should just
-- happen."). No task move is a coordinator step:
--
--   a harvest opens a card's PR (ns_harvest_pr, harvest.lua)
--       every task naming the PR or the card's origin issue -> merging
--   the stream lander merges a stream PR (ns_land_member, one call per member)
--       the CLOSE typed line on the member's pr record, and every task naming
--       the member PR or an issue its body closes -> landed
--   a person posts a CLOSE line (ns_read_post)                -> the same landing
--   a reader posts a SCORE line (ns_read_post)
--       the PR's read task read-<n>-<head8> working -> merging (the read is done)
--
-- The tasks an event names come from the ref index (01_task_ref.lua): one
-- SMEMBERS per ref, never a scan. Every move goes through the one task move
-- (NS.task.move, 02_card_move.lua, nova-tools #3778): the record and its sets
-- change together or the task is skipped by name with the move's refusal
-- and nothing is written; the score (the task's age) is kept and ws:log gets
-- one receipt per step. An event edge the graph takes in steps (ready ->
-- working -> merging when the work's PR opens; ready, parked or waiting ->
-- ... -> landed at a merge) is walked step by step, each step on the graph.
-- Exports NS.tev for harvest.lua.

local TE = {
  TR = NS.tref,
  -- the steps from each where to the event's where
  PATH = {
    merging = { waiting = { 'ready', 'working', 'merging' }, ready = { 'working', 'merging' },
      working = { 'merging' } },
    landed = { waiting = { 'ready', 'working', 'landed' }, ready = { 'working', 'landed' },
      working = { 'landed' }, merging = { 'landed' }, parked = { 'ready', 'working', 'landed' } },
  },
}

function TE.now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- TE.move(id, to, by, why, sha) -> 'MOVED' | 'SAME' | 'SKIP', and for SKIP
-- why. sha is the merge a landing is at.
function TE.move(id, to, by, why, sha)
  local p = NS.task.read(id)
  if not p then return 'SKIP', 'no task' end
  if p.stream == '' then return 'SKIP', 'no stream' end
  local from = p.where
  if not p.placed then from = NS.task.where_of[p.state] or p.state end
  if from == to then return 'SAME' end
  local path = TE.PATH[to][from]
  if not path then return 'SKIP', from .. '->' .. to .. ' is not an event move' end
  local err = NS.task.check(id, path[1], { by = by, why = why, sha = sha })
  if err then return 'SKIP', err end
  for _, step in ipairs(path) do
    err = NS.task.move(id, step, { by = by, why = why, sha = sha })
    if err then return 'SKIP', err end
  end
  return 'MOVED'
end

-- TE.apply(ids, to, by, why, sha) -> {moved, same, skipped, notes}: notes
-- are 'id: why' for each skipped id.
function TE.apply(ids, to, by, why, sha)
  local r = { moved = 0, same = 0, skipped = 0, notes = {} }
  for _, id in ipairs(ids) do
    local status, note = TE.move(id, to, by, why, sha)
    if status == 'MOVED' then
      r.moved = r.moved + 1
    elseif status == 'SAME' then
      r.same = r.same + 1
    else
      r.skipped = r.skipped + 1
      r.notes[#r.notes + 1] = id .. ': ' .. note
    end
  end
  return r
end

-- TE.refs(repo, n, closes) -> the refs 'repo#n' of a PR and the issues it
-- closes (closes: numbers, space-joined; '-' or '' for none).
function TE.refs(repo, n, closes)
  local bare = TE.TR.bare(repo)
  local refs = { bare .. '#' .. tonumber(n) }
  for i in string.gmatch(closes or '', '%d+') do refs[#refs + 1] = bare .. '#' .. tonumber(i) end
  return refs
end

-- TE.opened(refs, by, why): a PR naming these refs opened; its tasks move to
-- merging. Returns TE.apply's counts.
function TE.opened(refs, by, why)
  return TE.apply(TE.TR.ids(refs), 'merging', by, why)
end

-- TE.landed(refs, extra, by, why, sha): a merge (at sha) landed the work
-- these refs name; the tasks (and the extra ids, indexed first) move to
-- landed.
function TE.landed(refs, extra, by, why, sha)
  for _, id in ipairs(extra or {}) do TE.TR.index(id) end
  local ids = TE.TR.ids(refs)
  local seen = {}
  for _, id in ipairs(ids) do seen[id] = true end
  for _, id in ipairs(extra or {}) do
    if id ~= '' and not seen[id] and redis.call('EXISTS', 'task:' .. id) == 1 then
      seen[id] = true
      ids[#ids + 1] = id
    end
  end
  local r = TE.apply(ids, 'landed', by, why, sha)
  r.matched = #ids
  return r
end

function TE.prkey(repo, n)
  return 'pr:' .. TE.TR.bare(repo) .. ':' .. n
end

-- TE.line(key, line) adds one typed line to the pr record's two line stores,
-- the reads field the lander reads and the :lines list read post and the
-- read brief use, once. Returns 1 when it was new.
function TE.line(key, line)
  local reads = redis.call('HGET', key, 'reads') or ''
  local added = 0
  if ('\n' .. reads .. '\n'):find('\n' .. line .. '\n', 1, true) == nil then
    if reads ~= '' then reads = reads .. '\n' end
    redis.call('HSET', key, 'reads', reads .. line)
    added = 1
  end
  if not redis.call('LPOS', key .. ':lines', line) then
    redis.call('RPUSH', key .. ':lines', line)
    added = 1
  end
  return added
end

-- ns_land_member(repo, slug, merge_sha, n, task, line, by, why, closes)
-- One member of a merged stream landing, fenced by the landing: land:<repo>:
-- <slug> must be merged at merge_sha (STALE otherwise, nothing written). It
-- writes the CLOSE line on pr:<repo>:<n> (both line stores, once), stores the
-- issues the member closes (closes: numbers space-joined, '-' none, ''
-- unknown: the record's closes field stands), and moves every task naming the
-- member PR or one of those issues, and the member's own task, to landed with
-- why. Idempotent: a re-run adds no line and moves nothing twice.
--   {'OK', moved, same, skipped, matched, line_added, note...} | {'STALE', why}
redis.register_function('ns_land_member', function(keys, args)
  local repo, slug, sha, n, task = args[1] or '', args[2] or '', args[3] or '', args[4] or '', args[5] or ''
  local line, by, why, closes = args[6] or '', args[7] or '', args[8] or '', args[9] or ''
  if repo == '' or slug == '' or sha == '' or not string.match(n, '^[1-9]%d*$') or line == '' then
    return { 'USAGE', 'repo slug merge_sha n task line by why closes' }
  end
  local land = redis.call('HMGET', 'land:' .. repo .. ':' .. slug, 'state', 'merge_sha')
  if land[1] ~= 'merged' or land[2] ~= sha then
    return { 'STALE', 'land:' .. repo .. ':' .. slug .. ' is ' .. tostring(land[1]) .. ' at ' .. tostring(land[2]) }
  end
  local key = TE.prkey(repo, n)
  local added = 0
  if redis.call('EXISTS', key) == 1 then
    added = TE.line(key, line)
    if closes ~= '' then
      redis.call('HSET', key, 'closes', closes)
    else
      closes = redis.call('HGET', key, 'closes') or ''
    end
  end
  local r = TE.landed(TE.refs(repo, n, closes), { task }, by, why, sha)
  local out = { 'OK', tostring(r.moved), tostring(r.same), tostring(r.skipped), tostring(r.matched), tostring(added) }
  for _, note in ipairs(r.notes) do out[#out + 1] = note end
  return out
end)

-- ns_read_post(repo, n, line, now_s): a typed line posted on a PR, and the
-- move it is an event for, in one call. The line is appended to
-- pr:<repo>:<n>:lines and the record's last_line/last_line_at are stamped
-- (the record must have a head: NOHEAD otherwise, nothing written). A CLOSE
-- line by a person (who not jev*) lands every task naming the PR or an issue
-- in the record's closes, why the line's "with <repo>#<n> (<sha>)" when it
-- has one; a SCORE line moves the read task read-<n>-<head8> working ->
-- merging.
--   {'OK', lines, kind, moved, same, skipped, note...} | {'NOHEAD', key}
redis.register_function('ns_read_post', function(keys, args)
  local repo, n, line, now = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
  local key = TE.prkey(repo, n)
  if (redis.call('HGET', key, 'head') or '') == '' then return { 'NOHEAD', key } end
  local lines = redis.call('RPUSH', key .. ':lines', line)
  local first = string.match(line, '^[^\n]*')
  redis.call('HSET', key, 'last_line', first, 'last_line_at', now)
  local kind = string.match(first, '^(%S+)') or ''
  local who = string.match(first, 'who=([^%s:;,]+)') or ''
  local head = string.match(first, 'head=(%x+)') or ''
  local r = { moved = 0, same = 0, skipped = 0, notes = {} }
  if kind == 'CLOSE' and who ~= '' and string.sub(who, 1, 3) ~= 'jev' then
    local with, sha = string.match(first, 'with (%S+#%d+) %((%x+)%)')
    local why = 'landed: CLOSE by ' .. who .. ' on ' .. TE.TR.bare(repo) .. '#' .. n
    if with then why = 'landed with ' .. with .. ' (' .. sha .. ')' end
    -- the landing's merge: the line's sha, else the CLOSE line itself
    r = TE.landed(TE.refs(repo, n, redis.call('HGET', key, 'closes') or ''), {}, who, why,
      sha or ('close:' .. who .. ':' .. head))
  elseif kind == 'SCORE' and #head >= 8 then
    local id = 'read-' .. n .. '-' .. string.sub(head, 1, 8)
    if redis.call('HGET', 'task:' .. id, 'state') == 'working' then
      r = TE.apply({ id }, 'merging', who, 'read: SCORE by ' .. who .. ' at ' .. string.sub(head, 1, 8))
    end
  end
  local out = { 'OK', tostring(lines), kind, tostring(r.moved), tostring(r.same), tostring(r.skipped) }
  for _, note in ipairs(r.notes) do out[#out + 1] = note end
  return out
end)

NS.tev = TE
