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
-- SMEMBERS per ref, never a scan. Every move goes through TE.move, the task
-- side of the one move: the id must be in the ws set its record names and in
-- no other set of its stream (else the task is skipped by name and nothing is
-- written), the set changes with the record's state in the same call, the
-- score (the task's age) is kept, and ws:log gets one receipt.
--
-- TE.move writes the same keys as ws.lua's ns_ws_move with the two event
-- edges that graph lacks (any live state -> landed by a merge; waiting,
-- ready, working -> merging when the work's PR opens). It folds into the card
-- model's one writer (02_card_move.lua) when tasks become cards there.
-- Exports NS.tev for harvest.lua.

local TE = {
  TR = NS.tref,
  STATES = { 'waiting', 'ready', 'working', 'merging', 'landed', 'parked' },
  FROM = {
    merging = { waiting = true, ready = true, working = true },
    landed = { waiting = true, ready = true, working = true, merging = true, parked = true },
  },
}

function TE.now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- TE.move(id, to, by, why) -> 'MOVED' | 'SAME' | 'SKIP', and for SKIP why.
function TE.move(id, to, by, why)
  local f = redis.call('HMGET', 'task:' .. id, 'stream', 'state')
  local stream, from = f[1] or '', f[2] or ''
  if stream == '' then return 'SKIP', 'no stream' end
  if from == to then return 'SAME' end
  if not TE.FROM[to][from] then return 'SKIP', from .. '->' .. to .. ' is not an event move' end
  local score = redis.call('ZSCORE', 'ws:' .. stream .. ':' .. from, id)
  if not score then return 'SKIP', 'says ' .. from .. ' but is not in ws:' .. stream .. ':' .. from end
  for _, st in ipairs(TE.STATES) do
    if st ~= from and redis.call('ZSCORE', 'ws:' .. stream .. ':' .. st, id) then
      return 'SKIP', 'says ' .. from .. ' but is also in ws:' .. stream .. ':' .. st
    end
  end
  local now = TE.now()
  redis.call('ZREM', 'ws:' .. stream .. ':' .. from, id)
  redis.call('ZADD', 'ws:' .. stream .. ':' .. to, score, id)
  redis.call('HSET', 'task:' .. id, 'state', to, 'state_at', tostring(now), 'why', why or '')
  redis.call('XADD', 'ws:log', 'MAXLEN', '~', '200000', '*', 'id', id, 'stream', stream,
    'from', from, 'to', to, 'by', by or '', 'why', why or '', 'at', tostring(now))
  return 'MOVED'
end

-- TE.apply(ids, to, by, why) -> {moved, same, skipped, notes}: notes are
-- 'id: why' for each skipped id.
function TE.apply(ids, to, by, why)
  local r = { moved = 0, same = 0, skipped = 0, notes = {} }
  for _, id in ipairs(ids) do
    local status, note = TE.move(id, to, by, why)
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

-- TE.landed(refs, extra, by, why): a merge landed the work these refs name;
-- the tasks (and the extra ids, indexed first) move to landed.
function TE.landed(refs, extra, by, why)
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
  local r = TE.apply(ids, 'landed', by, why)
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
  local r = TE.landed(TE.refs(repo, n, closes), { task }, by, why)
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
    r = TE.landed(TE.refs(repo, n, redis.call('HGET', key, 'closes') or ''), {}, who, why)
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
