-- The deal pass's writes for nova-tools #3063 (#2756 3.2 queued <-> dealt,
-- 5.3; the pass itself is #2743). No shebang: loader.go prepends the single
-- library header. Every call below checks the reconciler's fencing token
-- against lease:reconciler (#2726) before any write, in the same call, and
-- refuses FENCED with the holder's instance and host; a Redis Function cannot
-- call another, so the check is inline (see reconciler.lua).
--
-- ns_card_deal is ONE call per bench per pass for the whole batch, whatever
-- sprints its cards come from (the pass shares a bench across open sprints,
-- so the sprint travels with each card, not once per call). ns_card_undeal is
-- one call per returned batch. ns_bench_ssh writes the dealer's ssh cell;
-- ns_card_why writes the launcher's refusal line on each refused card
-- (#3700).
-- ns_card_deal_fail is ns_card_deal's companion for a session that failed
-- before anything ran (#3322): ONE call writes the ssh cell, returns the
-- batch to the pool and counts the bench's consecutive timeouts on the cell.
-- At cfg:fleet ssh_fail_after (default 3) of them the fleet duty
-- (ns_fleet_step, fleet.lua, the one writer of bench:<b>:state) marks the
-- bench PROBING at its next step and holds it out of the deal until its beats
-- earn UP again; this file only reports that the count is there.
-- ns_card_gate is one call per sprint per pass for the DEPENDS-ON gate
-- (#3066): pool <-> waiting.
--
-- Where the dealer's ssh state lives (#3063 ruling): bench:<b>:ssh, a hash
-- {state, why, at}, written ONLY by ns_bench_ssh from the deal pass. The
-- bench's own beat (bench:<b>:beat, rewritten whole every second by the bench
-- itself) keeps its own ssh field for the bench's view; the dealer never
-- writes the beat and the beat never writes bench:<b>:ssh, so each value has
-- one writer (#2756 2.1 rule 4). The table line for the cell is #3045's.

local DEAL_LEASE = 'lease:reconciler'
-- Every card state write here is NS.card (02_card_move.lua): deal is
-- ready -> working, undeal working -> ready, gate ready <-> waiting.
local CARD = NS.card

local function deal_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- nil when token holds the lease, else the FENCED reply.
local function deal_fence(token)
  if token ~= nil and token ~= '' and redis.call('HGET', DEAL_LEASE, 'token') == token then
    return nil
  end
  local h = redis.call('HMGET', DEAL_LEASE, 'instance', 'host')
  return { 'FENCED', h[1] or '', h[2] or '' }
end

local function deal_receipt(S, kind, id, from_state, to_state, attempt, token_sha, actor, reason, idem, at)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
    'attempt', tostring(attempt or 0), 'token_sha', token_sha or '',
    'actor', actor or '', 'reason', reason or '', 'evidence', '',
    'idem', idem or '', 'at', tostring(at))
end

-- The bench profile: an empty legs field runs every leg, and a card with no
-- leg deals to any bench. THE ONE RULE for an empty legs list, the same in
-- ci.lua's ci_carries (#3349); `capacity bench --legs` writes the list. A missing field is the empty set: HMGET answers it
-- with Lua false, never nil (nova-tools #3321: the fleet's benches carry no
-- legs field and `card push` stores no leg, so gmatch on false failed every
-- deal).
local function deal_runs(legs, leg)
  if not leg or leg == '' or not legs or legs == '' then
    return true
  end
  for l in string.gmatch(legs, '[^, ]+') do
    if l == leg then
      return true
    end
  end
  return false
end

local function deal_names(list, name)
  for item in string.gmatch(list or '', '%S+') do
    if item == name then return true end
  end
  return false
end

-- The retry cap (nova-tools #3700): cfg:deal max_attempts, default 3. A
-- missing, non-numeric or non-positive value is the default, never
-- unbounded: sprint quack-0925 redealt the same 12 refused cards to attempt
-- 23 in twelve minutes.
local DEAL_MAX_ATTEMPTS = 3
-- A why on the card or the bench row is one line, capped.
local DEAL_WHY_CAP = 300

local function deal_max_attempts()
  local v = math.floor(tonumber(redis.call('HGET', 'cfg:deal', 'max_attempts') or '') or 0)
  if v < 1 then
    return DEAL_MAX_ATTEMPTS
  end
  return v
end

-- A card whose next attempt would exceed the cap leaves the pool for
-- done/fail (state refused) through the one move, reason retries, why the
-- last refusal line the deal pass wrote on it (or, when none, the attempts and
-- the last reason), and one receipt; it is never dealt again. c is the
-- card_deal HMGET below. Returns true when the card was capped.
local function deal_cap(S, label, ck, c, tried, max, actor, idem, at)
  local why = c[9]
  if not why or why == '' then
    why = 'retries: ' .. tried .. ' attempts reached cfg:deal max_attempts ' .. max ..
      '; last reason ' .. tostring(c[10] or '-')
  end
  why = string.sub(why, 1, DEAL_WHY_CAP)
  if CARD.move(ck, 'done', { state = 'refused', ok = 'fail', by = actor, why = 'retries',
      fields = { 'reason', 'retries', 'why', why, 'why_at', tostring(at), 'refused_at', tostring(at) } }) then
    return false
  end
  local pin = c[3] or ''
  if pin ~= '' then
    redis.call('ZREM', 's:' .. S .. ':bench:' .. pin .. ':queue', label)
  end
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', 'card retries', 'id', label, 'from', 'queued', 'to', 'refused',
    'attempt', tostring(tried), 'token_sha', '', 'actor', actor or '', 'reason', 'retries',
    'evidence', why, 'idem', idem or '', 'at', tostring(at))
  return true
end

-- Backpressure for one sprint, read once per call (#2756 5.3): ON flows only
-- the priority tier; a missing hash applies the declared policy.
local function deal_backpressure(S)
  local state = redis.call('HGET', 's:' .. S .. ':backpressure', 'state')
  if state then
    return state == 'ON'
  end
  return redis.call('HGET', 's:' .. S .. ':policy', 'backpressure_missing') == 'closed'
end

-- ns_card_deal(bench, token, actor, idem, then per card: S, label, attempt,
-- card_token, card_token_sha)
-- Moves up to free cards queued -> dealt on bench in one call (#2756 3.2 row
-- 3). attempt is the caller's attempt+1 read before the call and card_token
-- is <attempt>.<128 random bits hex> from the Go RNG (spec 2.1 rule 8, as
-- ns_task_take); card_token and card_token_sha are checked against that full
-- shape (32 lowercase hex after the attempt, a 12 lowercase hex sha), the
-- same match ns_task_take runs on its token before any write, not just the
-- <attempt>. prefix. A card whose attempt moved, that is no longer queued and
-- pooled, whose sprint is not open or is pit-stopped, that is pinned to another bench, whose
-- leg the bench does not run, whose tier backpressure holds, or whose token
-- or token_sha does not match that shape, is skipped. A queued, pooled card
-- of an open sprint whose attempt would exceed cfg:deal max_attempts is not
-- dealt: deal_cap moves it to done/fail (#3700) and it takes no slot.
-- The bench guard (registered, UP, not paused, not role friends, free > 0)
-- is checked once; free caps the batch. A bench whose registry role is
-- friends (#3634: the Studio hosts friends, the fleet runs swarms) is dealt
-- no swarm card: NONE role=friends. Returns FENCED, NONE <why>, or DEALT
-- followed by S, label, attempt, token for each dealt card.
local function card_deal(keys, args)
  local bench, token, actor, idem = args[1], args[2], args[3], args[4]
  local fenced = deal_fence(token)
  if fenced then
    return fenced
  end
  if (#args - 4) % 5 ~= 0 then
    return redis.error_reply('ns_card_deal: cards are S, label, attempt, token, token_sha')
  end
  if redis.call('SISMEMBER', 'benches', bench) == 0 then
    return { 'NONE', 'unregistered' }
  end
  if redis.call('EXISTS', 'bench:' .. bench .. ':reset') == 1 then
    return { 'NONE', 'resetting' }
  end
  local bstate = redis.call('HGET', 'bench:' .. bench .. ':state', 'state')
  if not bstate or bstate == '' then
    bstate = 'down'
  else
    bstate = string.lower(bstate)
  end
  if bstate ~= 'up' then
    return { 'NONE', bstate }
  end
  local desired = redis.call('HMGET', 'bench:' .. bench .. ':desired', 'slots', 'paused', 'legs', 'role')
  if desired[4] == 'friends' then
    return { 'NONE', 'role=friends' }
  end
  if desired[2] == '1' or desired[2] == 'true' then
    return { 'NONE', 'paused' }
  end
  local starting_key = 'bench:' .. bench .. ':starting'
  local free = (tonumber(desired[1]) or 0) -
    redis.call('ZCARD', starting_key) - redis.call('ZCARD', 'bench:' .. bench .. ':living')
  if free <= 0 then
    return { 'NONE', 'full' }
  end

  local at = deal_now_ms()
  local out = { 'DEALT' }
  local open, bp = {}, {}
  local max_attempts = deal_max_attempts()
  for i = 5, #args, 5 do
    if free <= 0 then
      break
    end
    local S, label, attempt, ctoken, csha = args[i], args[i + 1], tonumber(args[i + 2]), args[i + 3], args[i + 4]
    if open[S] == nil then
      -- A pit-stopped sprint (s:<S>:pitstop, pitstop.lua) deals nothing.
      open[S] = redis.call('HGET', 's:' .. S, 'status') == 'open' and
        redis.call('EXISTS', 's:' .. S .. ':pitstop') == 0
      bp[S] = deal_backpressure(S)
    end
    local ck = 's:' .. S .. ':card:' .. label
    local c = redis.call('HMGET', ck, 'state', 'attempt', 'bench', 'leg', 'tier', 'base_sha', 'avoid', 'priority',
      'why', 'reason')
    -- In the pool (scored by created_at, like every view) is dealable; the
    -- bench queue keeps the card's deal priority, the record's field.
    local score = redis.call('ZSCORE', 's:' .. S .. ':pool', label) and (tonumber(c[8]) or 0)
    local pin = c[3] or ''
    if open[S] and c[1] == 'queued' and score and attempt and attempt > max_attempts and
        attempt == (tonumber(c[2]) or 0) + 1 then
      deal_cap(S, label, ck, c, attempt - 1, max_attempts, actor, idem, at)
    elseif open[S] and c[1] == 'queued' and score and attempt and
        attempt == (tonumber(c[2]) or 0) + 1 and
        string.sub(ctoken, 1, #tostring(attempt) + 1) == tostring(attempt) .. '.' and
        string.match(ctoken, '^%d+%.[0-9a-f]+$') and #ctoken == #tostring(attempt) + 33 and
        string.match(csha, '^[0-9a-f]+$') and #csha == 12 and
		(pin == '' or pin == bench) and not deal_names(c[7], bench) and deal_runs(desired[3], c[4]) and
        (not bp[S] or c[5] == 'priority') and
        not CARD.move(ck, 'working', { state = 'dealt', bench = bench, by = actor, why = 'deal',
          fields = { 'attempt', tostring(attempt), 'token', ctoken, 'token_sha', csha, 'pin', pin,
            'identity', S .. '/' .. label .. '/' .. string.sub(c[6] or '', 1, 8) .. '/' .. bench .. '/' .. attempt,
            'dealt_at', tostring(at) } }) then
      redis.call('ZADD', starting_key, at, S .. '/' .. label .. '/' .. attempt)
      redis.call('ZADD', 's:' .. S .. ':bench:' .. bench .. ':queue', score, label)
      deal_receipt(S, 'card deal', label, 'queued', 'dealt', attempt, csha, actor, '', idem, at)
      out[#out + 1] = S
      out[#out + 1] = label
      out[#out + 1] = tostring(attempt)
      out[#out + 1] = ctoken
      free = free - 1
    end
  end
  return out
end

-- deal_undeal returns the dealt reservations named in args from `first` (S,
-- label, attempt triples) to ready on bench: NS.card moves the card (#3695:
-- the one writer of card state; the pool is a view of `where` and the deal
-- priority is the record's priority field) dealt -> queued with the token
-- cleared so a late child with it is refused, retries+1 and the reason on
-- the card, a pin kept as its bench; then the slot is freed, one receipt per
-- card and one cap:log slot-freed event per call. A card that is not dealt
-- on this bench under exactly this attempt, or one the move refuses (drift:
-- the move is its first write, so it writes nothing), is skipped, so a
-- repeat writes nothing. Returns the count returned.
local function deal_undeal(bench, reason, actor, idem, args, first, at)
  local n = 0
  for i = first, #args, 3 do
    local S, label, attempt = args[i], args[i + 1], args[i + 2]
    local ck = 's:' .. S .. ':card:' .. label
    local c = redis.call('HMGET', ck, 'state', 'bench', 'attempt', 'token_sha', 'pin', 'retries', 'priority')
    local pin = c[5] or ''
    local qk = 's:' .. S .. ':bench:' .. bench .. ':queue'
    if c[1] == 'dealt' and c[2] == bench and c[3] == attempt and
        not CARD.move(ck, 'ready', { state = 'queued', bench = pin, by = actor, why = reason,
          priority = tonumber(redis.call('ZSCORE', qk, label) or c[7]) or 0,
          fields = { 'token', '', 'reason', reason, 'retries', tostring((tonumber(c[6]) or 0) + 1) } }) then
      redis.call('ZREM', 'bench:' .. bench .. ':starting', S .. '/' .. label .. '/' .. attempt)
      if pin ~= bench then
        redis.call('ZREM', qk, label)
      end
      deal_receipt(S, 'card undeal', label, 'dealt', 'queued', attempt, c[4], actor, reason, idem, at)
      n = n + 1
    end
  end
  if n > 0 then
    redis.call('XADD', 'cap:log', '*',
      'kind', 'slot-freed', 'target', 'bench:' .. bench, 'slots', tostring(n),
      'reason', reason, 'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
  end
  return n
end

-- ns_card_undeal(bench, token, reason, actor, idem, then per card: S, label,
-- attempt)
-- Returns dealt reservations that never reached the bench to queued (#2756
-- 3.2 dealt -> queued; reason ssh-refused from the deal pass, spawn-timeout
-- from the reconciler), as deal_undeal does. Returns FENCED or UNDEALT <n>.
local function card_undeal(keys, args)
  local bench, token, reason, actor, idem = args[1], args[2], args[3], args[4], args[5]
  local fenced = deal_fence(token)
  if fenced then
    return fenced
  end
  if (#args - 5) % 3 ~= 0 then
    return redis.error_reply('ns_card_undeal: cards are S, label, attempt')
  end
  if reason == nil or reason == '' then
    return redis.error_reply('ns_card_undeal: reason is required')
  end
  local n = deal_undeal(bench, reason, actor, idem, args, 6, deal_now_ms())
  return { 'UNDEALT', tostring(n) }
end

-- ns_bench_ssh(bench, token, state, why)
-- The deal pass's ssh cell for one bench: bench:<b>:ssh {state, why, at},
-- at from Redis TIME in ms. Fenced; state is one of ok, refused, timeout,
-- error; the bench must be registered. Returns FENCED or OK <at>.
local function bench_ssh(keys, args)
  local bench, token, state, why = args[1], args[2], args[3], args[4]
  local fenced = deal_fence(token)
  if fenced then
    return fenced
  end
  if state ~= 'ok' and state ~= 'refused' and state ~= 'timeout' and state ~= 'error' then
    return redis.error_reply('ns_bench_ssh: state must be ok, refused, timeout or error')
  end
  if redis.call('SISMEMBER', 'benches', bench) == 0 then
    return redis.error_reply('ns_bench_ssh: bench ' .. tostring(bench) .. ' is not registered')
  end
  local at = deal_now_ms()
  local timeouts = redis.call('HGET', 'bench:' .. bench .. ':ssh', 'timeouts') or '0'
  if state == 'ok' then
    timeouts = '0'
  end
  why = string.gsub(string.sub(why or '', 1, DEAL_WHY_CAP), '[\r\n]', ' ')
  redis.call('HSET', 'bench:' .. bench .. ':ssh', 'state', state, 'why', why, 'at', tostring(at),
    'timeouts', timeouts)
  return { 'OK', tostring(at) }
end

-- ns_card_deal_fail(bench, token, state, why, actor, idem, then per card: S,
-- label, attempt)
-- ns_card_deal's companion for a bench whose session failed before anything
-- ran (nova-tools #3322: refused, or timeout when the ssh child was killed
-- at its deadline with no start line from the remote verb). ONE fenced call:
-- the ssh cell bench:<b>:ssh {state, why, at, timeouts} is written, the
-- batch is returned to the pool (deal_undeal, reason ssh-<state>), and a
-- timeout adds one to the bench's consecutive timeouts (a refusal keeps the
-- count, an ok row clears it). hold is 1 when the timeouts since the fleet
-- duty last held the bench (bench:<b>:state ssh_held, fleet.lua's) reach
-- cfg:fleet ssh_fail_after (default 3): ns_fleet_step flips the bench UP ->
-- PROBING at its next step, so the caller plans it nothing more this pass.
-- Every call is one bench-ssh receipt on cap:log (the flip's fleet-state
-- receipt is the step's). state is refused or timeout. Returns FENCED or
-- FAILED <at> <undealt> <timeouts> <bench state> <hold>.
local function card_deal_fail(keys, args)
  local bench, token, state, why, actor, idem = args[1], args[2], args[3], args[4], args[5], args[6]
  local fenced = deal_fence(token)
  if fenced then
    return fenced
  end
  if state ~= 'refused' and state ~= 'timeout' then
    return redis.error_reply('ns_card_deal_fail: state must be refused or timeout')
  end
  if (#args - 6) % 3 ~= 0 then
    return redis.error_reply('ns_card_deal_fail: cards are S, label, attempt')
  end
  if redis.call('SISMEMBER', 'benches', bench) == 0 then
    return redis.error_reply('ns_card_deal_fail: bench ' .. tostring(bench) .. ' is not registered')
  end
  local at = deal_now_ms()
  why = string.gsub(string.sub(why or '', 1, DEAL_WHY_CAP), '[\r\n]', ' ')
  local ssh_key = 'bench:' .. bench .. ':ssh'
  local timeouts = tonumber(redis.call('HGET', ssh_key, 'timeouts')) or 0
  if state == 'timeout' then
    timeouts = timeouts + 1
  end
  redis.call('HSET', ssh_key, 'state', state, 'why', why or '', 'at', tostring(at), 'timeouts', tostring(timeouts))
  local n = deal_undeal(bench, 'ssh-' .. state, actor, idem, args, 7, at)
  local fail_after = tonumber(redis.call('HGET', 'cfg:fleet', 'ssh_fail_after')) or 3
  if fail_after < 1 then
    fail_after = 1
  end
  local bs = redis.call('HMGET', 'bench:' .. bench .. ':state', 'state', 'ssh_held')
  local bstate, held = bs[1] or '', tonumber(bs[2]) or 0
  if timeouts < held then
    held = 0
  end
  local hold = '0'
  if state == 'timeout' and timeouts - held >= fail_after then
    hold = '1'
  end
  redis.call('XADD', 'cap:log', '*',
    'kind', 'bench-ssh', 'subject', bench, 'state', state, 'why', why or '',
    'timeouts', tostring(timeouts), 'returned', tostring(n), 'bench_state', bstate, 'hold', hold,
    'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
  return { 'FAILED', tostring(at), tostring(n), tostring(timeouts), bstate, hold }
end

-- ns_card_gate(token, S, actor, idem, then per card: label, verb, why)
-- The deal pass's DEPENDS-ON gate for one sprint in one call (nova-tools
-- #3066, #2756 3.2 `card release`). verb `wait` moves a queued, pooled card
-- to s:<S>:waiting (its priority stays on the record, why in wait_why), or
-- rewrites wait_why on a card already waiting; verb `release` moves a queued,
-- waiting card back to the pool at its priority and clears wait_why. A card
-- that is not queued, or not where the verb expects it, is skipped, so a
-- repeat writes nothing. ns_card_deal deals only pooled cards, so a waiting
-- card cannot be dealt. Returns FENCED, NONE <why>, or GATED <waited>
-- <released>.
local function card_gate(keys, args)
  local token, S, actor, idem = args[1], args[2], args[3], args[4]
  local fenced = deal_fence(token)
  if fenced then
    return fenced
  end
  if (#args - 4) % 3 ~= 0 then
    return redis.error_reply('ns_card_gate: cards are label, verb, why')
  end
  if redis.call('HGET', 's:' .. S, 'status') ~= 'open' then
    return { 'NONE', 'sprint not open' }
  end
  local at = deal_now_ms()
  local pool, waiting = 's:' .. S .. ':pool', 's:' .. S .. ':waiting'
  local waited, released = 0, 0
  for i = 5, #args, 3 do
    local label, verb, why = args[i], args[i + 1], args[i + 2]
    local ck = 's:' .. S .. ':card:' .. label
    local c = redis.call('HMGET', ck, 'state', 'priority', 'wait_why', 'attempt')
    if c[1] == 'queued' then
      if verb == 'wait' then
        local score = redis.call('ZSCORE', pool, label)
        if score and not CARD.move(ck, 'waiting', { by = actor, why = why,
            fields = { 'wait_why', why or '' } }) then
          deal_receipt(S, 'card wait', label, 'queued', 'queued', c[4], '', actor, why, idem, at)
          waited = waited + 1
        elseif redis.call('SISMEMBER', waiting, label) == 1 and c[3] ~= why then
          redis.call('HSET', ck, 'wait_why', why or '')
        end
      elseif verb == 'release' then
        if redis.call('SISMEMBER', waiting, label) == 1 and
            not CARD.move(ck, 'ready', { by = actor, why = 'depends-on landed' }) then
          redis.call('HDEL', ck, 'wait_why')
          deal_receipt(S, 'card release', label, 'queued', 'queued', c[4], '', actor, 'depends-on landed', idem, at)
          released = released + 1
        end
      else
        return redis.error_reply('ns_card_gate: verb must be wait or release')
      end
    end
  end
  return { 'GATED', tostring(waited), tostring(released) }
end

-- ns_card_why(token, then per card: S, label, attempt, why)
-- The deal pass's refusal line on each card it names (#3700): the card
-- record's why (capped, one line) and why_at, written only when the card is
-- still at that attempt, so a late write never lands on a newer attempt. The
-- line is what the bench's `card launch --stdin` printed for the card
-- (REFUSED line=<n> ...), or the session's own why when the batch never ran.
-- Fenced. Returns FENCED or WHY <n>.
local function card_why(keys, args)
  local token = args[1]
  local fenced = deal_fence(token)
  if fenced then
    return fenced
  end
  if (#args - 1) % 4 ~= 0 then
    return redis.error_reply('ns_card_why: cards are S, label, attempt, why')
  end
  local at = deal_now_ms()
  local n = 0
  for i = 2, #args, 4 do
    local S, label, attempt, why = args[i], args[i + 1], args[i + 2], args[i + 3]
    local ck = 's:' .. S .. ':card:' .. label
    if why ~= '' and redis.call('HGET', ck, 'attempt') == attempt then
      why = string.gsub(string.sub(why, 1, DEAL_WHY_CAP), '[\r\n]', ' ')
      redis.call('HSET', ck, 'why', why, 'why_at', tostring(at))
      n = n + 1
    end
  end
  return { 'WHY', tostring(n) }
end

redis.register_function('ns_card_deal', card_deal)
redis.register_function('ns_card_why', card_why)
redis.register_function('ns_card_gate', card_gate)
redis.register_function('ns_card_undeal', card_undeal)
redis.register_function('ns_card_deal_fail', card_deal_fail)
redis.register_function('ns_bench_ssh', bench_ssh)

-- ns_deal_status_list(S) is `deal status` list mode (#3605): one row per
-- member of benches, sorted by name, each {bench, beat exists, desired slots,
-- desired paused, starting, living, sprint queue, ssh state, ssh at}. Read
-- only (no-writes, called with FCALL_RO), so the bench seat, which may FCALL
-- but never EVAL, runs it. Its callers line for rowan-tools redis-acl-gen:
-- ns_deal_status_list internal/nsprint/deal/status.go ns-bench ns-coordinator
redis.register_function{
  function_name = 'ns_deal_status_list',
  flags = { 'no-writes' },
  callback = function(keys, args)
    local S = args[1]
    local benches = redis.call('SMEMBERS', 'benches')
    table.sort(benches)
    local res = {}
    for _, b in ipairs(benches) do
      local des = redis.call('HMGET', 'bench:' .. b .. ':desired', 'slots', 'paused', 'legs')
      local ssh = redis.call('HMGET', 'bench:' .. b .. ':ssh', 'state', 'at')
      res[#res + 1] = {
        b,
        NS.beat.up('bench:' .. b .. ':beat'),
        des[1] or '0',
        des[2] or '',
        redis.call('ZCARD', 'bench:' .. b .. ':starting'),
        redis.call('ZCARD', 'bench:' .. b .. ':living'),
        redis.call('ZCARD', 's:' .. S .. ':bench:' .. b .. ':queue'),
        ssh[1] or '',
        ssh[2] or '',
      }
    end
    return res
  end,
}
