-- task_live: the path-lint snapshot for task push (nova-tools #3067). One
-- read-only call returns whether the pushed id already exists (the push
-- function then answers EXISTS, CLOSED or CONFLICT, never the lint), then
-- every live (open, claimed or working) task of every sprint in sprint:order
-- plus the pushing sprint, as flat rows of sprint, id, kind, repo, title. The
-- state indexes are only candidates: a row is returned only when its hash
-- says the task is live. Nothing is written.
local function task_live(keys, args)
  local push_sprint, push_id = args[1], args[2]
  local sprints = redis.call('ZRANGE', 'sprint:order', 0, -1)
  local seen = {}
  local order = {}
  for _, s in ipairs(sprints) do
    if not seen[s] then
      seen[s] = true
      order[#order + 1] = s
    end
  end
  if push_sprint ~= '' and not seen[push_sprint] then
    order[#order + 1] = push_sprint
  end
  local out = { tostring(redis.call('EXISTS', 's:' .. push_sprint .. ':task:' .. push_id)) }
  local live = { open = true, claimed = true, working = true }
  for _, s in ipairs(order) do
    local done = {}
    for _, idx in ipairs({ 'open', 'claimed', 'working' }) do
      for _, id in ipairs(redis.call('SMEMBERS', 's:' .. s .. ':idx:task:' .. idx)) do
        if not done[id] then
          done[id] = true
          local row = redis.call('HMGET', 's:' .. s .. ':task:' .. id, 'state', 'kind', 'repo', 'title')
          if row[1] and live[row[1]] then
            out[#out + 1] = s
            out[#out + 1] = id
            out[#out + 1] = row[2] or ''
            out[#out + 1] = row[3] or ''
            out[#out + 1] = row[4] or ''
          end
        end
      end
    end
  end
  return out
end

redis.register_function{ function_name = 'ns_task_live', callback = task_live,
  flags = { 'no-writes' } }

-- Review tasks wait for CI at the exact head (#3093, spec 3.1, 10.12, control
-- 52). ok-to-friend creates them in waiting-ci when ci:<repo>:<head> is not
-- OK, and in no open:<f>. ns_ci_end writing OK opens them to the reader in
-- this same call; FAIL or a head change cancels them and cuts one fix task.
-- The read-enqueued clock is enqueued_at, written here from Redis TIME at the
-- moment the task enters open, which on the pending path is the OK call.

local function wci_now()
  local t = redis.call('TIME')
  return string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
end

local function wci_receipt(S, kind, id, from_state, to_state, attempt, token_sha, actor, reason, evidence, idem, at)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
    'attempt', tostring(attempt or 0), 'token_sha', token_sha or '',
    'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
    'idem', idem or '', 'at', tostring(at))
end

local function wci_hget(key, field)
  local v = redis.call('HGET', key, field)
  if not v then return '' end
  return tostring(v)
end

local function wci_create(S, id, reader, title, repo, pr, head, ref, priority, payload_sha, actor, at, mode)
  local key = 's:' .. S .. ':task:' .. id
  local state = 'waiting-ci'
  if mode == 'open' then state = 'open' end
  redis.call('HSET', key,
    'kind', 'review', 'repo', repo, 'ref', ref or '', 'pr', pr, 'head', head,
    'title', title, 'effects', 'none', 'owner', '', 'priority', tostring(priority),
    'state', state, 'attempt', '0', 'token', '0', 'payload_sha', payload_sha,
    'reason', '', 'evidence', '', 'claimed_at', '', 'started_at', '',
    'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '',
    'reader', reader)
  local score = -(tonumber(priority) or 0)
  if mode == 'open' then
    redis.call('HSET', key, 'enqueued_at', at)
    redis.call('ZADD', 's:' .. S .. ':open:' .. reader, score, id)
    redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
    wci_receipt(S, 'task', id, '', 'open', 0, '', actor, 'ci-ok', '', 'review-place:' .. id, at)
  else
    redis.call('SADD', 's:' .. S .. ':waiting-ci', id)
    redis.call('SADD', 's:' .. S .. ':idx:task:waiting-ci', id)
    wci_receipt(S, 'task', id, '', 'waiting-ci', 0, '', actor, 'ci-pending', '', 'review-place:' .. id, at)
  end
end

local function wci_matching(S, repo, pr, head)
  local out = {}
  for _, id in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':waiting-ci')) do
    local row = redis.call('HMGET', 's:' .. S .. ':task:' .. id, 'state', 'repo', 'pr', 'head')
    if row[1] == 'waiting-ci' and row[2] == repo and tostring(row[3]) == tostring(pr) and row[4] == head then
      out[#out + 1] = id
    end
  end
  return out
end

local function wci_move_open(S, id, actor, at)
  local key = 's:' .. S .. ':task:' .. id
  local reader = wci_hget(key, 'reader')
  local priority = tonumber(wci_hget(key, 'priority')) or 0
  redis.call('HSET', key, 'state', 'open', 'enqueued_at', at)
  redis.call('SREM', 's:' .. S .. ':waiting-ci', id)
  redis.call('SREM', 's:' .. S .. ':idx:task:waiting-ci', id)
  redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
  if reader ~= '' then
    redis.call('ZADD', 's:' .. S .. ':open:' .. reader, -priority, id)
  else
    redis.call('ZADD', 's:' .. S .. ':ready', -priority, id)
  end
  wci_receipt(S, 'task', id, 'waiting-ci', 'open', 0, '', actor, 'ci-ok', '', 'ci-ok:' .. id, at)
end

local function wci_cancel(S, id, reason, actor, at)
  local key = 's:' .. S .. ':task:' .. id
  local reader = wci_hget(key, 'reader')
  redis.call('HSET', key, 'state', 'cancelled', 'reason', reason, 'closed_at', at)
  redis.call('SREM', 's:' .. S .. ':waiting-ci', id)
  redis.call('SREM', 's:' .. S .. ':idx:task:waiting-ci', id)
  redis.call('SREM', 's:' .. S .. ':idx:task:open', id)
  if reader ~= '' then
    redis.call('ZREM', 's:' .. S .. ':open:' .. reader, id)
  end
  redis.call('ZREM', 's:' .. S .. ':ready', id)
  redis.call('SADD', 's:' .. S .. ':idx:task:cancelled', id)
  wci_receipt(S, 'task', id, 'waiting-ci', 'cancelled', 0, '', actor, reason, '', 'cancel:' .. reason .. ':' .. id, at)
end

-- One fix task per (pr, head, pkg), the 10.5 dedup key. A second call finds
-- the id and writes nothing.
local function wci_fix_id(repo, pr, head, pkg)
  if pkg == nil or pkg == '' then pkg = 'ci' end
  return 'fix-' .. repo .. '-' .. tostring(pr) .. '-' .. string.sub(head, 1, 12) .. '-' .. pkg, pkg
end

local function wci_cut_fix(S, repo, pr, head, pkg, author, actor, reason, at)
  local id, used = wci_fix_id(repo, pr, head, pkg)
  local key = 's:' .. S .. ':task:' .. id
  if redis.call('EXISTS', key) == 1 then
    return id
  end
  local dedup = tostring(pr) .. ':' .. head .. ':ci-fail:' .. used
  redis.call('HSET', key,
    'kind', 'fix', 'repo', repo, 'ref', '', 'pr', tostring(pr), 'head', head,
    'title', 'fix ' .. repo .. '#' .. tostring(pr) .. ' ' .. reason .. ' ' .. used .. ' at ' .. string.sub(head, 1, 12),
    'effects', 'idempotent', 'owner', '', 'priority', '1',
    'state', 'open', 'attempt', '0', 'token', '0', 'payload_sha', 'ci-fail:' .. dedup,
    'reason', reason, 'evidence', '', 'claimed_at', '', 'started_at', '',
    'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '',
    'reader', author, 'enqueued_at', at)
  redis.call('HSETNX', 's:' .. S .. ':unresolved', dedup,
    'ci-fail ' .. repo .. ' ' .. head .. ' ' .. used)
  if author ~= '' and redis.call('SISMEMBER', 'friends', author) == 1 then
    redis.call('ZADD', 's:' .. S .. ':open:' .. author, -1, id)
  else
    redis.call('ZADD', 's:' .. S .. ':ready', -1, id)
  end
  redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
  wci_receipt(S, 'task', id, '', 'open', 0, '', actor, reason, dedup, 'ci-fail:' .. dedup, at)
  return id
end

local function wci_rows(args, n)
  local rows = {}
  local base = 7
  for i = 0, n - 1 do
    local off = base + i * 5
    rows[#rows + 1] = {
      id = args[off + 1], reader = args[off + 2], title = args[off + 3],
      priority = tonumber(args[off + 4]) or 0, sha = args[off + 5],
    }
  end
  return rows
end

-- ns_review_place: ok-to-friend's review placement. One call creates every
-- required read at this head. OK opens them; anything else parks them.
local function review_place(keys, args)
  local S, repo, pr, head, ref, actor = args[1], args[2], args[3], args[4], args[5], args[6]
  local n = tonumber(args[7])
  if S == nil or S == '' or repo == nil or repo == '' or head == nil or head == '' or n == nil or n < 1 then
    return { 'INVALID', 'args' }
  end
  if wci_hget('s:' .. S .. ':pr:' .. repo .. ':' .. pr, 'head') ~= head then
    return { 'INVALID', 'head' }
  end
  local rows = wci_rows(args, n)
  for _, row in ipairs(rows) do
    if row.id == nil or row.id == '' or row.reader == nil or row.reader == '' then
      return { 'INVALID', 'reader' }
    end
    if redis.call('SISMEMBER', 'friends', row.reader) == 0 then
      return { 'INVALID', 'reader ' .. row.reader }
    end
  end
  for _, row in ipairs(rows) do
    local existing = wci_hget('s:' .. S .. ':task:' .. row.id, 'payload_sha')
    if existing ~= '' and existing ~= row.sha then
      return { 'CONFLICT', row.id }
    end
  end
  local verdict = wci_hget('ci:' .. repo .. ':' .. head, 'verdict')
  local mode = 'waiting-ci'
  if verdict == 'OK' then mode = 'open' end
  local at = wci_now()
  local created = 0
  for _, row in ipairs(rows) do
    if wci_hget('s:' .. S .. ':task:' .. row.id, 'payload_sha') == '' then
      wci_create(S, row.id, row.reader, row.title or '', repo, pr, head, ref, row.priority, row.sha, actor, at, mode)
      created = created + 1
    end
  end
  return { 'PLACED', mode, tostring(created) }
end

-- ns_ci_end: write the terminal verdict and settle waiting reviews for that
-- exact head in the same call. OK opens them (enqueued_at is this TIME).
-- FAIL cancels them and cuts one fix task.
local function ci_end(keys, args)
  local S, repo, pr, head = args[1], args[2], args[3], args[4]
  local verdict, pkg, author, actor = args[5], args[6] or '', args[7] or '', args[8] or ''
  if verdict ~= 'OK' and verdict ~= 'FAIL' then
    return { 'RECORD' }
  end
  if S == nil or S == '' or repo == nil or head == nil or head == '' then
    return { 'INVALID', 'args' }
  end
  local ids = wci_matching(S, repo, pr, head)
  local rec = 'ci:' .. repo .. ':' .. head
  local prev = wci_hget(rec, 'verdict')
  local fix_id = ''
  if verdict == 'FAIL' then
    fix_id = wci_fix_id(repo, pr, head, pkg)
  end
  if prev == verdict and #ids == 0 then
    return { verdict, '0', fix_id }
  end
  local at = wci_now()
  redis.call('HSET', rec,
    'verdict', verdict, 'repo', repo, 'pr', tostring(pr), 'head', head,
    'pkg', pkg, 'end_at', at, 'at', at, 'source', 'ns_ci_end')
  local reason = 'ci-fail'
  if verdict == 'OK' then
    for _, id in ipairs(ids) do
      wci_move_open(S, id, actor, at)
    end
  else
    for _, id in ipairs(ids) do
      wci_cancel(S, id, reason, actor, at)
    end
    fix_id = wci_cut_fix(S, repo, pr, head, pkg, author, actor, reason, at)
  end
  wci_receipt(S, 'ci end', repo .. ':' .. head, prev, verdict, 0, '', actor, verdict, rec,
    'ci-end:' .. head .. ':' .. verdict, at)
  return { verdict, tostring(#ids), fix_id }
end

-- ns_review_head_change: a new head cancels reviews still waiting on the old
-- one and cuts one fix task. It does not write ci:<repo>:<head>.
local function review_head_change(keys, args)
  local S, repo, pr, old_head, new_head = args[1], args[2], args[3], args[4], args[5]
  local author, actor = args[6] or '', args[7] or ''
  if S == nil or old_head == nil or old_head == '' or old_head == new_head then
    return { 'NONE', '0', '' }
  end
  local ids = wci_matching(S, repo, pr, old_head)
  if #ids == 0 then
    return { 'NONE', '0', '' }
  end
  local at = wci_now()
  for _, id in ipairs(ids) do
    wci_cancel(S, id, 'head-changed', actor, at)
  end
  local fix_id = wci_cut_fix(S, repo, pr, old_head, 'head-changed', author, actor, 'head-changed', at)
  return { 'CANCELLED', tostring(#ids), fix_id }
end

redis.register_function('ns_review_place', review_place)
redis.register_function('ns_ci_end', ci_end)
redis.register_function('ns_review_head_change', review_head_change)
