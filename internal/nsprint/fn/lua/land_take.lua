-- The fenced stream-PR lander (nova-tools #2942 rev 6): nova-sprint land
-- offer/run/list land stream PRs (stream/<slug> -> <base>) oldest first on
-- four facts at the head, one atomic git push fenced at the receiver
-- (refs/nova-land/<base>), landed only on a first-parent merge commit. These
-- functions are the only writers of the keys below; the Go side
-- (internal/nsprint/land/fenced) does the git work and calls them.
--
--   s:<S>:land:queue:<repo>:<base>   zset  <n> -> the stream PR's created_at (unix s)
--   s:<S>:land:queues                set   <repo>:<base>, the index (never SCANned)
--   s:<S>:pr:<repo>:<n>              hash  stream, base, created_at, body_first, offered_by,
--                                          state (landable, landing, landed, dropped),
--                                          skip_reason, lane, lane_at, lane_gen, land_head,
--                                          merge_sha, pushed_at, push_run, landed_at, drop_reason
--   s:<S>:land:run:<id>              hash  repo, base, runner, gen, state (taken, pushed,
--                                          landed, dropped), members, skipped, merge_shas,
--                                          open, taken_at, pushed_at, landed_at, reason, at
--   s:<S>:land:runs, s:<S>:land:open zset  run ids by taken_at; runs in pushed by pushed_at
--   s:<S>:land:last:<repo>:<base>    string the row's last run (a lapsed run is found here)
--   s:<S>:land:landed                zset  <repo>#<n> -> landed_at (ms)
--   s:<S>:land:writer:<repo>:<base>  hash  lease {gen, runner, run, at}, PEXPIRE 120000
--   land:gen                         string one counter for every sprint, no TTL
--   s:<S>:land:enq:<repo>:<n>:<head> string the gen that owns the claim on that head
--
-- Facts read here (never written): ci:<repo>:<head>:gids and
-- ci:<repo>:<head>:<gid> verdict (every gid OK, none is MISSING), and the
-- #3092 hold records s:<S>:hold:<unit>:<holder> through
-- s:<S>:prunit:<name>:<n> and the friends set.

local LEASE_MS = 120000
local STALE_MS = 7200000

local function now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function k_queue(S, R, B) return 's:' .. S .. ':land:queue:' .. R .. ':' .. B end
local function k_pr(S, R, n) return 's:' .. S .. ':pr:' .. R .. ':' .. n end
local function k_run(S, id) return 's:' .. S .. ':land:run:' .. id end
local function k_writer(S, R, B) return 's:' .. S .. ':land:writer:' .. R .. ':' .. B end
local function k_enq(S, R, n, H) return 's:' .. S .. ':land:enq:' .. R .. ':' .. n .. ':' .. H end

-- body_word: the first word of a body line with leading whitespace and
-- markdown (* _ # > `) dropped, trailing markdown and one run of : or .
-- stripped. Case-sensitive: HOLD and BLOCKED gate, Holds does not.
local function body_word(line)
  local s = line or ''
  s = string.gsub(s, '^[%s%*_#>`]+', '')
  local w = string.match(s, '^(%S+)') or ''
  w = string.gsub(w, '[%*_`:%.]+$', '')
  return w
end

local function ci_reason(R, H)
  local gids = redis.call('SMEMBERS', 'ci:' .. R .. ':' .. H .. ':gids')
  if #gids == 0 then
    return 'ci:MISSING'
  end
  table.sort(gids)
  for _, gid in ipairs(gids) do
    local v = redis.call('HGET', 'ci:' .. R .. ':' .. H .. ':' .. gid, 'verdict')
    if v ~= 'OK' then
      return 'ci:' .. ((v and v ~= '') and v or 'MISSING')
    end
  end
  return ''
end

local function hold_reason(S, R, n, H)
  local name = string.match(R, '([^/]+)$') or R
  local unit = redis.call('GET', 's:' .. S .. ':prunit:' .. name .. ':' .. n)
  if not unit or unit == '' then
    return ''
  end
  local friends = redis.call('SMEMBERS', 'friends')
  table.sort(friends)
  for _, f in ipairs(friends) do
    local h = redis.call('HMGET', 's:' .. S .. ':hold:' .. unit .. ':' .. f, 'head', 'released_by')
    local hh, rb = h[1], h[2]
    if hh and hh ~= '' and (not rb or rb == '') then
      if hh == H or (#hh >= 7 and string.match(hh, '^%x+$') and string.sub(H, 1, #hh) == hh) then
        return 'hold:' .. f
      end
    end
  end
  return ''
end

local function body_reason(S, R, n)
  local w = body_word(redis.call('HGET', k_pr(S, R, n), 'body_first'))
  if w == 'HOLD' or w == 'BLOCKED' then
    return 'body:' .. w
  end
  return ''
end

local function append(key, field, item)
  local cur = redis.call('HGET', key, field) or ''
  if cur == '' then
    redis.call('HSET', key, field, item)
  else
    redis.call('HSET', key, field, cur .. ' ' .. item)
  end
end

-- lease_ok: the row's lease is live and held at gen by runner (runner ''
-- matches any holder at gen).
local function lease_ok(S, R, B, runner, gen)
  local w = redis.call('HMGET', k_writer(S, R, B), 'gen', 'runner')
  if not w[1] or w[1] ~= tostring(gen) then
    return false
  end
  return runner == '' or w[2] == runner
end

-- ns_land_offer S R B n stream created_unix created_at body_first as withdraw
local function offer(keys, args)
  local S, R, B, n = args[1], args[2], args[3], args[4]
  local stream, created, created_at, body, as, withdraw = args[5], args[6], args[7], args[8], args[9], args[10]
  if not S or S == '' or not R or R == '' or not B or B == '' or not n or n == '' then
    return redis.error_reply('ns_land_offer: sprint, repo, base and n are required')
  end
  local pkey, qkey = k_pr(S, R, n), k_queue(S, R, B)
  local state = redis.call('HGET', pkey, 'state') or ''
  if withdraw == '1' then
    redis.call('ZREM', qkey, n)
    local lh = redis.call('HGET', pkey, 'land_head') or ''
    if lh ~= '' then
      redis.call('DEL', k_enq(S, R, n, lh))
    end
    redis.call('HSET', pkey, 'state', 'dropped', 'drop_reason', 'withdrawn', 'offered_by', as or '')
    return { 'WITHDRAWN', state }
  end
  if state == 'landed' then
    return { 'LANDED', redis.call('HGET', pkey, 'merge_sha') or '' }
  end
  if not tonumber(created) then
    return redis.error_reply('ns_land_offer: created must be unix seconds')
  end
  redis.call('ZADD', qkey, 'NX', created, n)
  redis.call('SADD', 's:' .. S .. ':land:queues', R .. ':' .. B)
  redis.call('HSET', pkey, 'stream', stream or '', 'base', B, 'created_at', created_at or '',
    'body_first', body or '', 'offered_by', as or '', 'drop_reason', '')
  if state ~= 'landing' then
    redis.call('HSET', pkey, 'state', 'landable')
  end
  return { 'OFFERED', redis.call('ZSCORE', qkey, n) }
end

-- ns_land_take S R B runner max: the row's writer lease (renewed when this
-- runner holds it, a new gen from land:gen when it is free or lapsed), the
-- pass's run, and up to max offered numbers oldest first with body, stream
-- and state. A run is made only when something is offered, and a run that
-- pushed nothing is carried to the next pass of the same gen (a worker that
-- loops every second over a red stream makes one run, not one a second). A
-- lapsed run is dropped reason=lease-lapsed; a run pushed longer than the
-- policy's land_stale_after ms ago is dropped reason=pushed-not-seen.
local function take(keys, args)
  local S, R, B, runner, max = args[1], args[2], args[3], args[4], tonumber(args[5] or '8') or 8
  if not S or S == '' or not R or R == '' or not B or B == '' or not runner or runner == '' then
    return redis.error_reply('ns_land_take: sprint, repo, base and runner are required')
  end
  local wkey = k_writer(S, R, B)
  local w = redis.call('HMGET', wkey, 'gen', 'runner')
  local at = now_ms()
  local gen, fresh
  if w[1] and w[2] ~= runner then
    return { 'WRITER', w[2] or '', w[1] }
  end
  local lastkey = 's:' .. S .. ':land:last:' .. R .. ':' .. B
  local last = redis.call('GET', lastkey)
  local lv = { nil, nil }
  if last then
    lv = redis.call('HMGET', k_run(S, last), 'gen', 'state')
  end
  if w[1] then
    gen, fresh = tonumber(w[1]), '0'
  else
    gen, fresh = redis.call('INCR', 'land:gen'), '1'
    if lv[2] == 'taken' then
      redis.call('HSET', k_run(S, last), 'state', 'dropped', 'reason', 'lease-lapsed', 'at', tostring(at))
      lv[2] = 'dropped'
    end
  end
  local stale = tonumber(redis.call('HGET', 's:' .. S .. ':policy', 'land_stale_after') or '') or STALE_MS
  local open = 's:' .. S .. ':land:open'
  for _, old in ipairs(redis.call('ZRANGEBYSCORE', open, '-inf', at - stale)) do
    redis.call('HSET', k_run(S, old), 'state', 'dropped', 'reason', 'pushed-not-seen', 'at', tostring(at))
    redis.call('ZREM', open, old)
  end
  local ns = redis.call('ZRANGE', k_queue(S, R, B), 0, max - 1)
  local run = ''
  if #ns > 0 then
    if fresh == '0' and lv[1] == tostring(gen) and lv[2] == 'taken' then
      run = last
      redis.call('HSET', k_run(S, run), 'at', tostring(at))
    else
      run = tostring(gen) .. '-' .. tostring(at)
      redis.call('HSET', k_run(S, run), 'repo', R, 'base', B, 'runner', runner, 'gen', tostring(gen),
        'state', 'taken', 'taken_at', tostring(at), 'members', '', 'skipped', '', 'merge_shas', '',
        'open', '0', 'at', tostring(at))
      redis.call('ZADD', 's:' .. S .. ':land:runs', at, run)
      redis.call('SET', lastkey, run)
    end
  end
  redis.call('HSET', wkey, 'gen', tostring(gen), 'runner', runner, 'run', run, 'at', tostring(at))
  redis.call('PEXPIRE', wkey, LEASE_MS)
  local out = { 'OK', tostring(gen), fresh, run }
  for _, n in ipairs(ns) do
    local pkey = k_pr(S, R, n)
    redis.call('HSET', pkey, 'lane', runner, 'lane_at', tostring(at), 'lane_gen', tostring(gen))
    local p = redis.call('HMGET', pkey, 'body_first', 'stream', 'state')
    out[#out + 1] = n
    out[#out + 1] = p[1] or ''
    out[#out + 1] = p[2] or ''
    out[#out + 1] = p[3] or ''
  end
  return out
end

-- ns_land_renew / ns_land_release S R B runner gen: compare-and-act on the
-- lease; FENCED and no write unless it is held at gen by runner.
local function renew(keys, args)
  local S, R, B, runner, gen = args[1], args[2], args[3], args[4], args[5]
  if not lease_ok(S, R, B, runner, gen) then
    return { 'FENCED' }
  end
  redis.call('PEXPIRE', k_writer(S, R, B), LEASE_MS)
  return { 'OK', redis.call('PTTL', k_writer(S, R, B)) }
end

local function release(keys, args)
  local S, R, B, runner, gen = args[1], args[2], args[3], args[4], args[5]
  if not lease_ok(S, R, B, runner, gen) then
    return { 'FENCED' }
  end
  local run = redis.call('HGET', k_writer(S, R, B), 'run') or ''
  if run ~= '' and redis.call('HGET', k_run(S, run), 'state') == 'taken' then
    redis.call('HSET', k_run(S, run), 'state', 'dropped', 'reason', 'released', 'at', tostring(now_ms()))
  end
  redis.call('DEL', k_writer(S, R, B))
  return { 'OK' }
end

-- ns_land_facts S R n1 H1 n2 H2 ...: read only; per PR the ci, hold and
-- body reasons at that head ('' when the fact holds).
local function facts(keys, args)
  local S, R = args[1], args[2]
  local out = {}
  local i = 3
  while args[i] do
    local n, H = args[i], args[i + 1] or ''
    out[#out + 1] = ci_reason(R, H)
    out[#out + 1] = hold_reason(S, R, n, H)
    out[#out + 1] = body_reason(S, R, n)
    i = i + 2
  end
  return out
end

-- ns_land_enq_claim S R B n H runner gen: the claim on <n>@<H> before its
-- push. FENCED unless the lease is live at gen and lane_gen is gen; CI, HOLD
-- or BODY (with the reason) when a fact failed since the read; then OK
-- (claim set), DUP (this gen's own retry), TAKEOVER (a smaller gen's
-- unexecuted claim, now this gen's) or FENCED (a larger gen owns it).
local function enq_claim(keys, args)
  local S, R, B, n, H, runner, gen = args[1], args[2], args[3], args[4], args[5], args[6], args[7]
  if not lease_ok(S, R, B, runner, gen) or redis.call('HGET', k_pr(S, R, n), 'lane_gen') ~= tostring(gen) then
    return { 'FENCED' }
  end
  local r = ci_reason(R, H)
  if r ~= '' then return { 'CI', r } end
  r = hold_reason(S, R, n, H)
  if r ~= '' then return { 'HOLD', r } end
  r = body_reason(S, R, n)
  if r ~= '' then return { 'BODY', r } end
  local ekey = k_enq(S, R, n, H)
  local cur = tonumber(redis.call('GET', ekey) or '')
  local g = tonumber(gen)
  if not cur then
    redis.call('SET', ekey, tostring(gen))
    return { 'OK' }
  end
  if cur == g then
    return { 'DUP' }
  end
  if cur < g then
    redis.call('SET', ekey, tostring(gen))
    return { 'TAKEOVER', tostring(cur) }
  end
  return { 'FENCED' }
end

-- ns_land_mark S R B n gen run kind a1 a2: FENCED and no write unless gen is
-- the run's gen (and, with n, the PR's lane_gen). kinds:
--   skip    a1=reason a2=H   skip_reason and land_head; the PR stays offered
--   skips   a1=list          the run's skipped field (#n:<reason> ...), this pass's
--   landing a1=H a2=M        an accepted push (needs the live lease at gen)
--   landed  a1=H a2=M        the mirror's base has M with second parent H
local function mark(keys, args)
  local S, R, B, n, gen, run, kind, a1, a2 = args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9]
  local rkey = k_run(S, run or '')
  local rv = redis.call('HMGET', rkey, 'gen', 'state')
  if rv[1] ~= tostring(gen) or rv[2] == 'dropped' then
    return { 'FENCED' }
  end
  local pkey = ''
  if n and n ~= '' then
    pkey = k_pr(S, R, n)
    if redis.call('HGET', pkey, 'lane_gen') ~= tostring(gen) then
      return { 'FENCED' }
    end
  end
  local at = tostring(now_ms())
  if kind == 'skip' then
    redis.call('HSET', pkey, 'skip_reason', a1 or '', 'land_head', a2 or '')
    return { 'OK' }
  end
  if kind == 'skips' then
    redis.call('HSET', rkey, 'skipped', a1 or '', 'at', at)
    return { 'OK' }
  end
  if kind == 'landing' then
    if not lease_ok(S, R, B, '', gen) then
      return { 'FENCED' }
    end
    redis.call('HSET', pkey, 'state', 'landing', 'land_head', a1, 'merge_sha', a2, 'pushed_at', at,
      'push_run', run, 'skip_reason', '')
    append(rkey, 'members', '#' .. n .. '@' .. a1)
    append(rkey, 'merge_shas', a2)
    redis.call('HINCRBY', rkey, 'open', 1)
    redis.call('HSET', rkey, 'state', 'pushed', 'pushed_at', at, 'at', at)
    redis.call('ZADD', 's:' .. S .. ':land:open', at, run)
    return { 'OK' }
  end
  if kind == 'landed' then
    redis.call('HSET', pkey, 'state', 'landed', 'land_head', a1, 'merge_sha', a2, 'landed_at', at, 'skip_reason', '')
    redis.call('ZREM', k_queue(S, R, B), n)
    redis.call('ZADD', 's:' .. S .. ':land:landed', at, R .. '#' .. n)
    local prun = redis.call('HGET', pkey, 'push_run') or ''
    if prun ~= '' then
      local pk = k_run(S, prun)
      if redis.call('HGET', pk, 'state') == 'pushed' and redis.call('HINCRBY', pk, 'open', -1) <= 0 then
        redis.call('HSET', pk, 'state', 'landed', 'landed_at', at, 'at', at)
        redis.call('ZREM', 's:' .. S .. ':land:open', prun)
      end
    end
    return { 'OK' }
  end
  return redis.error_reply('ns_land_mark: unknown kind ' .. tostring(kind))
end

redis.register_function('ns_land_offer', offer)
redis.register_function('ns_land_take', take)
redis.register_function('ns_land_renew', renew)
redis.register_function('ns_land_release', release)
redis.register_function{ function_name = 'ns_land_facts', callback = facts, flags = { 'no-writes' } }
redis.register_function('ns_land_enq_claim', enq_claim)
redis.register_function('ns_land_mark', mark)
