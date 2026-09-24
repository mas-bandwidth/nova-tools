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
-- one call per returned batch. ns_bench_ssh writes the dealer's ssh cell.
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
-- leg deals to any bench. A missing field is the empty set: HMGET answers it
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
-- pooled, whose sprint is not open, that is pinned to another bench, whose
-- leg the bench does not run, whose tier backpressure holds, or whose token
-- or token_sha does not match that shape, is skipped.
-- The bench guard (registered, UP, not paused, free > 0) is checked once;
-- free caps the batch. Returns FENCED, NONE <why>, or DEALT followed by
-- S, label, attempt, token for each dealt card.
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
  if redis.call('EXISTS', 'bench:' .. bench .. ':beat') == 0 then
    return { 'NONE', 'down' }
  end
  local desired = redis.call('HMGET', 'bench:' .. bench .. ':desired', 'slots', 'paused', 'legs')
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
  for i = 5, #args, 5 do
    if free <= 0 then
      break
    end
    local S, label, attempt, ctoken, csha = args[i], args[i + 1], tonumber(args[i + 2]), args[i + 3], args[i + 4]
    if open[S] == nil then
      open[S] = redis.call('HGET', 's:' .. S, 'status') == 'open'
      bp[S] = deal_backpressure(S)
    end
    local ck = 's:' .. S .. ':card:' .. label
    local c = redis.call('HMGET', ck, 'state', 'attempt', 'bench', 'leg', 'tier', 'base_sha')
    local score = redis.call('ZSCORE', 's:' .. S .. ':pool', label)
    local pin = c[3] or ''
    if open[S] and c[1] == 'queued' and score and attempt and
        attempt == (tonumber(c[2]) or 0) + 1 and
        string.sub(ctoken, 1, #tostring(attempt) + 1) == tostring(attempt) .. '.' and
        string.match(ctoken, '^%d+%.[0-9a-f]+$') and #ctoken == #tostring(attempt) + 33 and
        string.match(csha, '^[0-9a-f]+$') and #csha == 12 and
        (pin == '' or pin == bench) and deal_runs(desired[3], c[4]) and
        (not bp[S] or c[5] == 'priority') then
      local identity = S .. '/' .. label .. '/' .. string.sub(c[6] or '', 1, 8) .. '/' .. bench .. '/' .. attempt
      redis.call('HSET', ck, 'state', 'dealt', 'attempt', tostring(attempt),
        'token', ctoken, 'token_sha', csha, 'bench', bench, 'pin', pin,
        'identity', identity, 'dealt_at', tostring(at))
      redis.call('ZREM', 's:' .. S .. ':pool', label)
      redis.call('SREM', 's:' .. S .. ':idx:card:queued', label)
      redis.call('SADD', 's:' .. S .. ':idx:card:dealt', label)
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

-- ns_card_undeal(bench, token, reason, actor, idem, then per card: S, label,
-- attempt)
-- Returns dealt reservations that never reached the bench to queued (#2756
-- 3.2 dealt -> queued; reason ssh-refused from the deal pass, spawn-timeout
-- from the reconciler): the card token is cleared so a late child with it is
-- refused, retries+1, the reason on the card, the slot freed, the card back
-- in the pool at the score it was dealt from, a pin kept, one receipt per
-- card and one cap:log slot-freed event per call. A card that is not dealt on
-- this bench under exactly this attempt is skipped, so a repeat writes
-- nothing. Returns FENCED or UNDEALT <n>.
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
  local at = deal_now_ms()
  local n = 0
  for i = 6, #args, 3 do
    local S, label, attempt = args[i], args[i + 1], args[i + 2]
    local ck = 's:' .. S .. ':card:' .. label
    local c = redis.call('HMGET', ck, 'state', 'bench', 'attempt', 'token_sha', 'pin', 'retries', 'priority')
    if c[1] == 'dealt' and c[2] == bench and c[3] == attempt then
      local pin = c[5] or ''
      local qk = 's:' .. S .. ':bench:' .. bench .. ':queue'
      local score = redis.call('ZSCORE', qk, label) or c[7] or '0'
      redis.call('HSET', ck, 'state', 'queued', 'token', '', 'bench', pin,
        'reason', reason, 'retries', tostring((tonumber(c[6]) or 0) + 1))
      redis.call('ZREM', 'bench:' .. bench .. ':starting', S .. '/' .. label .. '/' .. attempt)
      if pin ~= bench then
        redis.call('ZREM', qk, label)
      end
      redis.call('ZADD', 's:' .. S .. ':pool', score, label)
      redis.call('SREM', 's:' .. S .. ':idx:card:dealt', label)
      redis.call('SADD', 's:' .. S .. ':idx:card:queued', label)
      deal_receipt(S, 'card undeal', label, 'dealt', 'queued', attempt, c[4], actor, reason, idem, at)
      n = n + 1
    end
  end
  if n > 0 then
    redis.call('XADD', 'cap:log', 'MAXLEN', '~', '100000', '*',
      'kind', 'slot-freed', 'target', 'bench:' .. bench, 'slots', tostring(n),
      'reason', reason, 'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
  end
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
  redis.call('HSET', 'bench:' .. bench .. ':ssh', 'state', state, 'why', why or '', 'at', tostring(at))
  return { 'OK', tostring(at) }
end

-- ns_card_gate(token, S, actor, idem, then per card: label, verb, why)
-- The deal pass's DEPENDS-ON gate for one sprint in one call (nova-tools
-- #3066, #2756 3.2 `card release`). verb `wait` moves a queued, pooled card
-- to s:<S>:waiting (its pool score kept in priority, why in wait_why), or
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
        if score then
          redis.call('ZREM', pool, label)
          redis.call('SADD', waiting, label)
          redis.call('HSET', ck, 'priority', tostring(score), 'wait_why', why or '')
          deal_receipt(S, 'card wait', label, 'queued', 'queued', c[4], '', actor, why, idem, at)
          waited = waited + 1
        elseif redis.call('SISMEMBER', waiting, label) == 1 and c[3] ~= why then
          redis.call('HSET', ck, 'wait_why', why or '')
        end
      elseif verb == 'release' then
        if redis.call('SISMEMBER', waiting, label) == 1 then
          redis.call('SREM', waiting, label)
          redis.call('ZADD', pool, tonumber(c[2]) or 0, label)
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

redis.register_function('ns_card_deal', card_deal)
redis.register_function('ns_card_gate', card_gate)
redis.register_function('ns_card_undeal', card_undeal)
redis.register_function('ns_bench_ssh', bench_ssh)
