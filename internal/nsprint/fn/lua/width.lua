-- Width: declared slots, desired, deficit, fillstate per friend (nova-tools
-- #3071 rev 4). No shebang: loader.go prepends the single library header.
-- Every lua/ file shares one chunk, so this file declares exactly one chunk
-- local, WD, and hangs its helpers on it.
--
-- Keys:
--   friend:<f>:desired    the declared slots (capacity function, one writer).
--   friend:<f>:beat       TTL 5 s; up = key exists.
--   friend:<f>:starting   zset of claimed tasks.
--   friend:<f>:living     zset of tasks with beats.
--   friend:<f>:fillstate  one writer, ns_width_write (the width duty):
--                         slots, leased, working, deficit, eligible,
--                         idle_no_ready, idle_deps, idle_input, idle_unfilled,
--                         peak, peak_at, unfilled_since, starting, living, at.
--                         fill_at, fill_n written by ns_width_fill.
--                         eligible (recipient) and idle_unfilled (sender)
--                         consumed by ns_width_move as it moves, so the
--                         next move in the same pass sees the spare that
--                         is left, never the snapshot's (#3484 hold 1).

local WD = {}

function WD.now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

function WD.holds(token)
  return token ~= nil and token ~= '' and redis.call('HGET', 'lease:reconciler', 'token') == token
end

function WD.fenced()
  local h = redis.call('HMGET', 'lease:reconciler', 'instance', 'host')
  return { 'FENCED', h[1] or '', h[2] or '' }
end

function WD.receipt(S, kind, id, from_state, to_state, attempt, token_sha, actor, reason, evidence, idem, at)
  local log_key = 's:' .. S .. ':log'
  redis.call('XADD', log_key, '*',
    'kind', kind,
    'id', id,
    'from', from_state,
    'to', to_state,
    'attempt', tostring(attempt or 0),
    'token_sha', token_sha or '',
    'actor', actor or '',
    'reason', reason or '',
    'evidence', evidence or '',
    'idem', idem or '',
    'at', tostring(at)
  )
end

function WD.is_read(kind)
  return kind == 'read' or kind == 'review'
end

function WD.pr_producing(kind)
  return kind == 'work' or kind == 'fix' or kind == 'build'
end

function WD.is_dedup(S, f, repo, pr, head, exclude_id)
  if repo == '' or pr == '' or head == '' then
    return false
  end
  if redis.call('HEXISTS', 's:' .. S .. ':disp:' .. repo .. ':' .. pr, f .. '@' .. head) == 1 then
    return true
  end
  for _, tid in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':done:' .. f)) do
    if tid ~= exclude_id then
      local tk = 's:' .. S .. ':task:' .. tid
      if redis.call('HGET', tk, 'repo') == repo and redis.call('HGET', tk, 'pr') == pr and redis.call('HGET', tk, 'head') == head then
        return true
      end
    end
  end
  for _, zkey in ipairs({ 'friend:' .. f .. ':starting', 'friend:' .. f .. ':living' }) do
    for _, identity in ipairs(redis.call('ZRANGE', zkey, 0, -1)) do
      local s, tid = string.match(identity, '^([^/]+)/(.+)/%d+$')
      if s == S and tid ~= exclude_id then
        local tk = 's:' .. S .. ':task:' .. tid
        if redis.call('HGET', tk, 'repo') == repo and redis.call('HGET', tk, 'pr') == pr and redis.call('HGET', tk, 'head') == head then
          return true
        end
      end
    end
  end
  for _, tid in ipairs(redis.call('ZRANGE', 's:' .. S .. ':open:' .. f, 0, -1)) do
    if tid ~= exclude_id then
      local tk = 's:' .. S .. ':task:' .. tid
      if redis.call('HGET', tk, 'repo') == repo and redis.call('HGET', tk, 'pr') == pr and redis.call('HGET', tk, 'head') == head then
        return true
      end
    end
  end
  return false
end

-- WD.ready checks whether a task in sprint S is ready to be claimed or moved.
function WD.ready(S, key)
  local kind = redis.call('HGET', key, 'kind') or ''
  if WD.is_read(kind) then
    local repo = redis.call('HGET', key, 'repo') or ''
    local pr = redis.call('HGET', key, 'pr') or ''
    local head = redis.call('HGET', 's:' .. S .. ':pr:' .. repo .. ':' .. pr, 'head') or ''
    local want = redis.call('HGET', key, 'head') or ''
    if head == '' or want == '' then
      return false, 'head:missing'
    end
    if head ~= want then
      return false, 'head:moved'
    end
  end
  local deps = redis.call('HGET', key, 'depends_on') or ''
  if deps ~= '' and deps ~= '-' and deps ~= 'none' then
    for dep in string.gmatch(deps, '[^%s,]+') do
      if dep ~= '' and dep ~= '-' and dep ~= 'none' then
        local r, p = string.match(dep, '^([^#]+)#(%d+)$')
        if r and p then
          local pr_key = 's:' .. S .. ':pr:' .. r .. ':' .. p
          if redis.call('HGET', pr_key, 'merged') ~= '1' then
            return false, 'deps'
          end
          local pr_base = redis.call('HGET', pr_key, 'base') or ''
          local task_base = redis.call('HGET', key, 'base') or redis.call('HGET', 's:' .. S, 'base') or ''
          if pr_base ~= '' and task_base ~= '' and pr_base ~= task_base then
            return false, 'deps'
          end
        else
          local dk = 's:' .. S .. ':task:' .. dep
          if redis.call('EXISTS', dk) == 1 then
            if redis.call('HGET', dk, 'state') ~= 'closed' then
              return false, 'deps'
            end
            local pr = redis.call('HGET', dk, 'pr') or ''
            if pr ~= '' and pr ~= '0' then
              local repo = redis.call('HGET', dk, 'repo') or ''
              if redis.call('HGET', 's:' .. S .. ':pr:' .. repo .. ':' .. pr, 'merged') ~= '1' then
                return false, 'deps'
              end
            end
          else
            local ck = 's:' .. S .. ':card:' .. dep
            if redis.call('EXISTS', ck) == 1 then
              if redis.call('HGET', ck, 'state') ~= 'landed' then
                return false, 'deps'
              end
            else
              return false, 'deps'
            end
          end
        end
      end
    end
  end
  return true, ''
end

-- ns_width_write: writes friend:<f>:fillstate and READ-BOUND state under the reconciler lease fence.
-- args:
--   1: fence token (lease:reconciler)
--   2: read_bound ('1' or '0')
--   3: num_policy_readers
--   4 .. 3+num_policy_readers: policy reader names
--   next: num_open_sprints
--   next .. : open sprint names
--   next: num_friends
--   then 15 fields per friend:
--     f, slots, leased, working, deficit, eligible, idle_no_ready, idle_deps,
--     idle_input, idle_unfilled, peak, peak_at, unfilled_since, starting, living
local function width_write(keys, args)
  local fence = args[1]
  if not WD.holds(fence) then
    return WD.fenced()
  end
  local at = WD.now_ms()

  if args[2] == '0' or args[2] == '1' then
    local read_bound = args[2]
    local num_readers = tonumber(args[3] or '0') or 0
    local idx = 4
    for r_i = 1, num_readers do
      local r = args[idx]
      if r and r ~= '' then
        redis.call('SADD', 'width:readers', r)
      end
      idx = idx + 1
    end

    local num_sprints = tonumber(args[idx] or '0') or 0
    idx = idx + 1
    for s_i = 1, num_sprints do
      local S = args[idx]
      if S and S ~= '' then
        redis.call('HSETNX', 's:' .. S .. ':backpressure', 'state', 'OFF')
        redis.call('HSET', 's:' .. S .. ':backpressure', 'read_bound', read_bound, 'at', tostring(at))
      end
      idx = idx + 1
    end

    redis.call('SET', 'sprint:read_bound', read_bound)

    local num_friends = tonumber(args[idx] or '0') or 0
    idx = idx + 1
    for f_i = 1, num_friends do
      local f = args[idx]
      local slots = args[idx + 1] or '0'
      local leased = args[idx + 2] or '0'
      local working = args[idx + 3] or '0'
      local deficit = args[idx + 4] or '0'
      local eligible = args[idx + 5] or '0'
      local idle_no_ready = args[idx + 6] or '0'
      local idle_deps = args[idx + 7] or '0'
      local idle_input = args[idx + 8] or '0'
      local idle_unfilled = args[idx + 9] or '0'
      local peak = args[idx + 10] or '0'
      local peak_at = args[idx + 11] or '0'
      local unfilled_since = args[idx + 12] or '0'
      local starting = args[idx + 13] or '0'
      local living = args[idx + 14] or '0'
      idx = idx + 15

      local key = 'friend:' .. f .. ':fillstate'
      redis.call('HSET', key,
        'slots', slots,
        'leased', leased,
        'working', working,
        'deficit', deficit,
        'eligible', eligible,
        'idle_no_ready', idle_no_ready,
        'idle_deps', idle_deps,
        'idle_input', idle_input,
        'idle_unfilled', idle_unfilled,
        'peak', peak,
        'peak_at', peak_at,
        'unfilled_since', unfilled_since,
        'starting', starting,
        'living', living,
        'at', tostring(at)
      )
    end
  else
    local i = 2
    while i <= #args do
      local f = args[i]
      local slots = args[i + 1] or '0'
      local leased = args[i + 2] or '0'
      local working = args[i + 3] or '0'
      local deficit = args[i + 4] or '0'
      local eligible = args[i + 5] or '0'
      local idle_no_ready = args[i + 6] or '0'
      local idle_deps = args[i + 7] or '0'
      local idle_input = args[i + 8] or '0'
      local idle_unfilled = args[i + 9] or '0'
      local peak = args[i + 10] or '0'
      local peak_at = args[i + 11] or '0'
      local unfilled_since = args[i + 12] or '0'
      local starting = args[i + 13] or '0'
      local living = args[i + 14] or '0'
      i = i + 15

      local key = 'friend:' .. f .. ':fillstate'
      redis.call('HSET', key,
        'slots', slots,
        'leased', leased,
        'working', working,
        'deficit', deficit,
        'eligible', eligible,
        'idle_no_ready', idle_no_ready,
        'idle_deps', idle_deps,
        'idle_input', idle_input,
        'idle_unfilled', idle_unfilled,
        'peak', peak,
        'peak_at', peak_at,
        'unfilled_since', unfilled_since,
        'starting', starting,
        'living', living,
        'at', tostring(at)
      )
    end
  end
  return { 'OK' }
end

-- ns_width_fill: batch claim min(deficit, eligible, max) tasks for friend f.
-- args: f, sprint, max, actor, idem, random_token1, random_token2, ...
-- returns { 'OK', tostring(n), tostring(deficit_after), id1, kind1, ref1, token1, ... }
local function width_fill(keys, args)
  local f = args[1]
  local target_sprint = args[2] or ''
  local max_claim = tonumber(args[3] or '0') or 0
  local actor = args[4] or f
  if actor == '' then actor = f end
  local idem = args[5] or ''

  local dkey = 'friend:' .. f .. ':desired'
  if redis.call('EXISTS', dkey) == 0 then
    return redis.error_reply('friend ' .. f .. ' has no desired slots')
  end
  if redis.call('HGET', dkey, 'paused') == '1' then
    return { 'OK', '0', '0' }
  end
  if redis.call('SISMEMBER', 'friends', f) == 0 or redis.call('EXISTS', 'friend:' .. f .. ':beat') == 0 then
    return { 'OK', '0', '0' }
  end

  local slots = tonumber(redis.call('HGET', dkey, 'slots') or '0')
  if slots <= 0 then
    return { 'OK', '0', '0' }
  end

  local starting = redis.call('ZCARD', 'friend:' .. f .. ':starting')
  local living = redis.call('ZCARD', 'friend:' .. f .. ':living')
  local leased = starting + living
  local deficit = math.max(0, slots - leased)
  if deficit <= 0 then
    return { 'OK', '0', '0' }
  end

  local limit = deficit
  if max_claim > 0 and max_claim < limit then
    limit = max_claim
  end
  -- Every claim token carries one caller-supplied 128-bit random part (32
  -- lowercase hex). There is no fallback: a claim with no random part left
  -- is not made (fail closed), and a malformed part refuses the whole call
  -- before any write, so a token is never derivable from time or counts.
  local nrand = #args - 5
  if nrand < 0 then nrand = 0 end
  for i = 6, #args do
    if not string.match(args[i], '^[0-9a-f]+$') or #args[i] ~= 32 then
      return redis.error_reply('fill: random part ' .. (i - 5) .. ' is not 32 hex')
    end
  end
  if nrand < limit then
    limit = nrand
  end
  if limit <= 0 then
    return { 'OK', '0', tostring(deficit) }
  end

  local sprints = {}
  if target_sprint ~= '' then
    if redis.call('HGET', 's:' .. target_sprint, 'status') == 'open' then
      sprints[#sprints + 1] = target_sprint
    end
  else
    local order = redis.call('ZRANGE', 'sprint:order', 0, -1)
    local seen = {}
    for _, s in ipairs(order) do
      if not seen[s] and redis.call('HGET', 's:' .. s, 'status') == 'open' then
        seen[s] = true
        sprints[#sprints + 1] = s
      end
    end
    for _, s in ipairs(redis.call('SMEMBERS', 'sprints')) do
      if not seen[s] and redis.call('HGET', 's:' .. s, 'status') == 'open' then
        seen[s] = true
        sprints[#sprints + 1] = s
      end
    end
  end

  local claimed = {}
  local now = WD.now_ms()

  for _, S in ipairs(sprints) do
    if #claimed >= limit then break end
    local qkey = 's:' .. S .. ':open:' .. f
    local task_ids = redis.call('ZRANGE', qkey, 0, -1)
    for _, id in ipairs(task_ids) do
      if #claimed >= limit then break end
      local key = 's:' .. S .. ':task:' .. id
      if redis.call('EXISTS', key) == 1 and redis.call('HGET', key, 'state') == 'open' and redis.call('ZSCORE', qkey, id) then
        local ready = WD.ready(S, key)
        if ready then
          local repo = redis.call('HGET', key, 'repo') or ''
          local pr = redis.call('HGET', key, 'pr') or ''
          local head = redis.call('HGET', key, 'head') or ''
          if not WD.is_dedup(S, f, repo, pr, head, id) then
            local rand_part = args[6 + #claimed]
            local prev_attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
            local attempt = prev_attempt + 1
            local token = tostring(attempt) .. '.' .. rand_part
            local token_sha = string.sub(redis.sha1hex(token), 1, 12)

            redis.call('HSET', key,
              'state', 'claimed',
              'owner', f,
              'attempt', tostring(attempt),
              'token', token,
              'token_sha', token_sha,
              'claimed_at', tostring(now)
            )
            redis.call('ZREM', 's:' .. S .. ':ready', id)
            redis.call('ZREM', qkey, id)
            redis.call('SREM', 's:' .. S .. ':idx:task:open', id)
            redis.call('SADD', 's:' .. S .. ':idx:task:claimed', id)
            redis.call('ZADD', 'friend:' .. f .. ':starting', now, S .. '/' .. id .. '/' .. attempt)

            WD.receipt(S, 'task take', id, 'open', 'claimed', attempt, token_sha, actor, '', '', idem, now)

            local kind = redis.call('HGET', key, 'kind') or ''
            local ref = redis.call('HGET', key, 'ref') or ''
            if ref == '' then ref = redis.call('HGET', key, 'brief') or '' end
            if ref == '' then ref = id end

            claimed[#claimed + 1] = { S, id, kind, ref, token }
          end
        end
      end
    end
  end

  if #claimed > 0 then
    redis.call('HSET', 'friend:' .. f .. ':fillstate', 'fill_at', tostring(now), 'fill_n', tostring(#claimed))
  end

  local new_deficit = math.max(0, slots - (leased + #claimed))
  local out = { 'OK', tostring(#claimed), tostring(new_deficit) }
  for _, c in ipairs(claimed) do
    out[#out + 1] = c[2] -- id
    out[#out + 1] = c[3] -- kind
    out[#out + 1] = c[4] -- ref
    out[#out + 1] = c[5] -- token
  end
  return out
end

-- ns_width_move: rebalance eligible build/fix tasks from A to B.
-- args: fence, from_f, to_f, sprint, rebal_after_ms, actor, idem.
-- returns { 'OK', tostring(moved_count) }
local function width_move(keys, args)
  local fence = args[1]
  if not WD.holds(fence) then
    return WD.fenced()
  end
  local from_f = args[2]
  local to_f = args[3]
  local target_sprint = args[4] or ''
  local rebal_after_ms = tonumber(args[5] or '30000') or 30000
  local actor = args[6] or 'width'
  local idem = args[7] or ''

  local now = WD.now_ms()

  -- Condition 1: from_f has idle_unfilled > 0 for longer than rebalance_after
  local afs = redis.call('HGETALL', 'friend:' .. from_f .. ':fillstate')
  local afs_map = {}
  for i = 1, #afs, 2 do afs_map[afs[i]] = afs[i+1] end
  local a_unfilled = tonumber(afs_map['idle_unfilled'] or '0') or 0
  local a_unfilled_since = tonumber(afs_map['unfilled_since'] or '0') or 0
  if a_unfilled <= 0 or a_unfilled_since == 0 or (now - a_unfilled_since) <= rebal_after_ms then
    return { 'OK', '0' }
  end

  -- Condition 2: to_f is up
  if redis.call('EXISTS', 'friend:' .. to_f .. ':beat') == 0 then
    return { 'OK', '0' }
  end

  -- Condition 3: to_f fill_at is within rebalance_after of Redis TIME
  local bfs = redis.call('HGETALL', 'friend:' .. to_f .. ':fillstate')
  local bfs_map = {}
  for i = 1, #bfs, 2 do bfs_map[bfs[i]] = bfs[i+1] end
  local b_fill_at = tonumber(bfs_map['fill_at'] or '0') or 0
  if b_fill_at == 0 or (now - b_fill_at) > rebal_after_ms then
    return { 'OK', '0' }
  end

  -- Condition 4: to_f deficit > to_f eligible
  local b_deficit = tonumber(bfs_map['deficit'] or '0') or 0
  local b_eligible = tonumber(bfs_map['eligible'] or '0') or 0
  local b_spare = b_deficit - b_eligible
  if b_spare <= 0 then
    return { 'OK', '0' }
  end

  local max_move = math.min(a_unfilled, b_spare)
  if max_move <= 0 then
    return { 'OK', '0' }
  end

  local sprints = {}
  if target_sprint ~= '' then
    sprints[#sprints + 1] = target_sprint
  else
    local order = redis.call('ZRANGE', 'sprint:order', 0, -1)
    local seen = {}
    for _, s in ipairs(order) do
      if not seen[s] and redis.call('HGET', 's:' .. s, 'status') == 'open' then
        seen[s] = true
        sprints[#sprints + 1] = s
      end
    end
    for _, s in ipairs(redis.call('SMEMBERS', 'sprints')) do
      if not seen[s] and redis.call('HGET', 's:' .. s, 'status') == 'open' then
        seen[s] = true
        sprints[#sprints + 1] = s
      end
    end
  end

  local moved = 0
  for _, S in ipairs(sprints) do
    if moved >= max_move then break end
    local from_q = 's:' .. S .. ':open:' .. from_f
    local to_q = 's:' .. S .. ':open:' .. to_f
    local task_entries = redis.call('ZRANGE', from_q, 0, -1, 'WITHSCORES')
    for i = 1, #task_entries, 2 do
      if moved >= max_move then break end
      local id = task_entries[i]
      local score = tonumber(task_entries[i+1])
      local key = 's:' .. S .. ':task:' .. id
      if redis.call('EXISTS', key) == 1 and redis.call('HGET', key, 'state') == 'open' then
        local kind = redis.call('HGET', key, 'kind') or ''
        if kind == '' or kind == 'work' or kind == 'fix' or kind == 'build' then
          local ready = WD.ready(S, key)
          if ready then
            local repo = redis.call('HGET', key, 'repo') or ''
            local pr = redis.call('HGET', key, 'pr') or ''
            local head = redis.call('HGET', key, 'head') or ''
            if not WD.is_dedup(S, to_f, repo, pr, head, nil) then
              redis.call('ZREM', from_q, id)
              redis.call('ZADD', to_q, score, id)
              redis.call('HSET', key, 'owner', to_f)
              redis.call('XADD', 's:' .. S .. ':log', '*',
                'kind', 'width move',
                'id', id,
                'from', from_f,
                'to', to_f,
                'reason', 'underfull',
                'at', tostring(now)
              )
              moved = moved + 1
            end
          end
        end
      end
    end
  end

  if moved > 0 then
    -- Consume the spare this call used: the recipient's eligible grows by
    -- the tasks it received and the sender's idle_unfilled shrinks by the
    -- tasks it gave, so a later move in the same pass (another sender,
    -- another sprint) sees b_spare - moved, never the snapshot again.
    redis.call('HSET', 'friend:' .. to_f .. ':fillstate', 'eligible', tostring(b_eligible + moved))
    redis.call('HSET', 'friend:' .. from_f .. ':fillstate', 'idle_unfilled', tostring(math.max(0, a_unfilled - moved)))
  end

  return { 'OK', tostring(moved) }
end

redis.register_function('ns_width_write', width_write)
redis.register_function('ns_width_fill', width_fill)
redis.register_function('ns_width_move', width_move)
