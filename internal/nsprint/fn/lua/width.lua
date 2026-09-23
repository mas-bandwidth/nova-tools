-- Width: declared and measured slots, desired and deficit per friend, the
-- underfull rebalance, READ-BOUND and the atomic slot reservation (nova-tools
-- #3071 and Stella's controls in its first comment). No shebang: loader.go
-- prepends the single library header. Every lua/ file shares one chunk, so
-- this file declares exactly one chunk local, WD, and hangs its helpers on it.
--
-- Keys (all global to the consumer, summed over open sprints in sprint:order):
--   friend:<f>:desired  the declared slots (capacity function, one writer).
--   friend:<f>:width    one writer, ns_width_tick (and ns_width_reserve's gen):
--                       slots (effective = min(declared, slots_cap)), declared,
--                       slots_cap, cap (measured), working, starting,
--                       ready_open, blocked, desired, deficit, fillable,
--                       idle (typed per-slot reasons), stuck, under,
--                       working_ms, desired_ms, gen, woke, at.
--   friend:<f>:peaks    hour -> the largest working count held in that hour;
--                       cap is the max over the last 24 hours.
--   friend:<f>:wake     stream; UNDERFULL wakes for a harness with no loop.
--   sprint:width        the fleet line: working, desired, read_bound, at.
--   width:log           stream of per-friend samples (f, working, desired, at)
--                       the fold integrates for utilisation.
--
-- working counts only ACKed children (friend:<f>:living: a task becomes
-- WORKING on the child's first beat, task_beat.lua), never a take alone;
-- starting (claimed, not yet ACKed) is its own column, so
--   desired  = min(slots, working + starting + ready_open)
--   deficit  = desired - working          (from slots, not from "any WORKING")
--   fillable = desired - working - starting (what a take may start now)
-- ready_open counts only open tasks on the friend's queue whose DEPENDS-ON
-- are merged (#3066) and, for a read, whose head is the PR's known head.
--
-- Fencing (Stella's hold 3 on #3086): ns_width_tick always, and
-- ns_width_reserve when it is the reconciler dealing, take the
-- lease:reconciler token and refuse FENCED unless it is the stored one, in
-- the same call, before any write (the rule in reconciler.lua). The tick has
-- one writer: the reconciler pass (width.Duty); `nova-sprint width` takes the
-- lease for its one tick and refuses while a reconciler holds it.

local WD = {}

WD.HOUR_MS = 3600000
WD.PEAK_HOURS = 24

function WD.now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

function WD.csv(s)
  local out = {}
  for name in string.gmatch(s or '', '[^,]+') do
    out[#out + 1] = name
  end
  table.sort(out)
  return out
end

function WD.set(list)
  local out = {}
  for _, name in ipairs(list) do
    out[name] = true
  end
  return out
end

function WD.num(v)
  return tonumber(v or '0') or 0
end

function WD.open_sprints()
  local out = {}
  for _, S in ipairs(redis.call('ZRANGE', 'sprint:order', 0, -1)) do
    if redis.call('HGET', 's:' .. S, 'status') == 'open' then
      out[#out + 1] = S
    end
  end
  return out
end

-- PR-producing kinds: a build (work) or a fix opens a PR a reader must read.
function WD.pr_producing(kind)
  return kind == 'work' or kind == 'fix' or kind == 'build'
end

function WD.is_read(kind)
  return kind == 'read' or kind == 'review'
end

-- WD.ready: the #3066 rule as this file assumes it. A task names its
-- dependencies in the task hash field `depends_on` (task ids in the same
-- sprint, separated by spaces or commas). A dependency is met when its task is
-- closed and either carries no PR or its PR hash s:<S>:pr:<repo>:<pr> has
-- merged=1. A read is ready only at the PR's current head, and fails closed:
-- a PR hash with no head, or a read task with no head, cannot establish that
-- the read targets the head, so it is not ready (reason head:missing), never
-- dealt. Returns ready, reason (head:missing, head:moved, deps).
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
  for dep in string.gmatch(deps, '[^%s,]+') do
    local dk = 's:' .. S .. ':task:' .. dep
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
  end
  return true, ''
end

-- WD.holds: the lease:reconciler fence, checked inline (reconciler.lua).
function WD.holds(token)
  return token ~= nil and token ~= '' and redis.call('HGET', 'lease:reconciler', 'token') == token
end

function WD.fenced()
  local h = redis.call('HMGET', 'lease:reconciler', 'instance', 'host')
  return { 'FENCED', h[1] or '', h[2] or '' }
end

-- WD.why: the blocked reasons as one line, sorted ("deps=2 head:missing=1").
function WD.why(counts)
  local names = {}
  for name in pairs(counts) do names[#names + 1] = name end
  table.sort(names)
  local parts = {}
  for _, name in ipairs(names) do parts[#parts + 1] = name .. '=' .. counts[name] end
  return table.concat(parts, ' ')
end

-- WD.queue: the friend's open queue over open sprints, in sprint order then
-- queue order, split into ready entries, a blocked count and the blocked
-- reasons counted by name.
function WD.queue(f, sprints)
  local ready, blocked, why = {}, 0, {}
  for _, S in ipairs(sprints) do
    for _, id in ipairs(redis.call('ZRANGE', 's:' .. S .. ':open:' .. f, 0, -1)) do
      local key = 's:' .. S .. ':task:' .. id
      local ok, reason = false, 'state'
      if redis.call('HGET', key, 'state') == 'open' then
        ok, reason = WD.ready(S, key)
      end
      if ok then
        ready[#ready + 1] = { S = S, id = id, kind = redis.call('HGET', key, 'kind') or '' }
      else
        blocked = blocked + 1
        why[reason] = (why[reason] or 0) + 1
      end
    end
  end
  return ready, blocked, why
end

function WD.declared(f)
  local key = 'friend:' .. f .. ':desired'
  if redis.call('HGET', key, 'paused') == '1' then
    return 0
  end
  return WD.num(redis.call('HGET', key, 'slots'))
end

-- WD.effective: declared slots lowered to a CAP the tick wrote. A new hello
-- (desired.at newer than the cap) or working above the cap clears it.
function WD.effective(f, declared, working)
  local wkey = 'friend:' .. f .. ':width'
  local capped = redis.call('HGET', wkey, 'slots_cap')
  if not capped then
    return declared
  end
  local cap_at = WD.num(redis.call('HGET', wkey, 'slots_cap_at'))
  local hello_at = WD.num(redis.call('HGET', 'friend:' .. f .. ':desired', 'at'))
  capped = tonumber(capped)
  if hello_at > cap_at or working > capped then
    redis.call('HDEL', wkey, 'slots_cap', 'slots_cap_at')
    return declared
  end
  if capped < declared then
    return capped
  end
  return declared
end

-- WD.peak records working in this hour's bucket and returns the measured cap
-- before and after this sample (the largest working held in 24 hours).
function WD.peak(f, working, at)
  local key = 'friend:' .. f .. ':peaks'
  local hour = math.floor(at / WD.HOUR_MS)
  local before, after = 0, working
  local flat = redis.call('HGETALL', key)
  for i = 1, #flat, 2 do
    local h, v = tonumber(flat[i]), tonumber(flat[i + 1])
    if h <= hour - WD.PEAK_HOURS then
      redis.call('HDEL', key, flat[i])
    else
      if v > before then before = v end
      if v > after then after = v end
    end
  end
  local cur = WD.num(redis.call('HGET', key, tostring(hour)))
  if working > cur then
    redis.call('HSET', key, tostring(hour), tostring(working))
  end
  return before, after
end

function WD.present(f)
  return redis.call('SISMEMBER', 'friends', f) == 1 and
    redis.call('EXISTS', 'friend:' .. f .. ':beat') == 1 and
    redis.call('EXISTS', 'friend:' .. f .. ':state') == 0 and
    redis.call('HGET', 'friend:' .. f .. ':desired', 'paused') ~= '1'
end

function WD.mark(title, marker)
  if string.find(title, marker, 1, true) then
    return title
  end
  if title == '' then
    return marker
  end
  return title .. ' ' .. marker
end

function WD.move(S, id, from, to, reason, actor, idem, at)
  local key = 's:' .. S .. ':task:' .. id
  local score = redis.call('ZSCORE', 's:' .. S .. ':open:' .. from, id) or '0'
  redis.call('ZREM', 's:' .. S .. ':open:' .. from, id)
  redis.call('ZADD', 's:' .. S .. ':open:' .. to, score, id)
  local marker = '[moved from ' .. from .. ': ' .. reason .. ']'
  redis.call('HSET', key, 'title', WD.mark(redis.call('HGET', key, 'title') or '', marker))
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', 'task move', 'id', id, 'from', 'open', 'to', 'open',
    'attempt', redis.call('HGET', key, 'attempt') or '0', 'token_sha', '',
    'actor', actor or '', 'reason', reason, 'evidence', 'from=' .. from .. ' to=' .. to,
    'idem', idem or '', 'at', tostring(at))
end

-- WD.idle: one typed reason per slot the friend is not working:
-- starting (claimed, awaiting the child's ACK), fillable (ready now),
-- deps (open work waiting on unmerged DEPENDS-ON or a moved head),
-- no-work, capped (declared slots above the effective ones).
function WD.idle(declared, slots, working, starting, fillable, blocked)
  local empty = slots - working
  if empty < 0 then empty = 0 end
  local parts = {}
  local function take(name, n)
    if n > empty then n = empty end
    if n > 0 then
      parts[#parts + 1] = name .. '=' .. n
      empty = empty - n
    end
  end
  take('starting', starting)
  take('fillable', fillable)
  take('deps', blocked)
  take('no-work', empty)
  if declared > slots then
    parts[#parts + 1] = 'capped=' .. (declared - slots)
  end
  return table.concat(parts, ' ')
end

-- ns_width_tick: one reconciler tick over every registered friend.
-- args = rebalance_ticks, readers csv, builders csv, coordinator, actor, idem,
-- fence (the lease:reconciler token; any other refuses FENCED, nothing written).
-- Returns { rows, events }: a row per friend
--   { f, slots, cap, working, starting, ready_open, desired, deficit, fillable, idle }
-- and event lines CAP, MOVE, READ-BOUND, UNDERFULL.
local function width_tick(keys, args)
  if not WD.holds(args[7]) then
    return WD.fenced()
  end
  local rebalance_ticks = WD.num(args[1])
  if rebalance_ticks < 1 then
    return redis.error_reply('ns_width_tick: rebalance_ticks must be >= 1 (measured p95 take latency)')
  end
  local readers, builders = WD.csv(args[2]), WD.csv(args[3])
  local coordinator, actor, idem = args[4] or '', args[5], args[6]
  local builder_set = WD.set(builders)
  local at = WD.now_ms()
  local sprints = WD.open_sprints()
  local friends = redis.call('SMEMBERS', 'friends')
  table.sort(friends)

  local st, events = {}, {}
  for _, f in ipairs(friends) do
    local wkey = 'friend:' .. f .. ':width'
    local declared = WD.declared(f)
    local working = redis.call('ZCARD', 'friend:' .. f .. ':living')
    local starting = redis.call('ZCARD', 'friend:' .. f .. ':starting')
    local ready, blocked, why = WD.queue(f, sprints)
    local slots = WD.effective(f, declared, working)
    local cap_before, cap = WD.peak(f, working, at)

    local desired = math.min(slots, working + starting + #ready)
    local deficit = math.max(0, desired - working)
    local under = WD.num(redis.call('HGET', wkey, 'under'))
    if deficit > 0 then under = under + 1 else under = 0 end

    -- CAP: takes keep failing to raise working above the measured cap while
    -- ready work waits and the declared width is not yet held.
    local stuck = WD.num(redis.call('HGET', wkey, 'stuck'))
    if cap_before > 0 and working > 0 and working <= cap_before and
        working + starting < slots and #ready > 0 then
      stuck = stuck + 1
    else
      stuck = 0
    end
    if stuck >= rebalance_ticks then
      slots = cap
      redis.call('HSET', wkey, 'slots_cap', tostring(cap), 'slots_cap_at', tostring(at))
      events[#events + 1] = 'CAP ' .. f .. ' slots=' .. cap .. ' measured=' .. cap
      stuck = 0
      desired = math.min(slots, working + starting + #ready)
      deficit = math.max(0, desired - working)
    end
    local fillable = math.max(0, desired - working - starting)

    st[#st + 1] = {
      f = f, declared = declared, slots = slots, cap = cap, working = working,
      starting = starting, ready = ready, blocked = blocked, why = WD.why(why), desired = desired,
      deficit = deficit, fillable = fillable, under = under, stuck = stuck,
      present = WD.present(f),
    }
  end

  -- Underfull rebalance: a friend whose deficit persisted rebalance_ticks
  -- moves the ready builds and fixes it cannot start now (overflow) to the
  -- present friend with the most spare width (slots - desired). Reads stay
  -- with their readers; a WORKING or claimed task never moves.
  for _, a in ipairs(st) do
    local overflow = #a.ready - a.fillable
    if a.under >= rebalance_ticks and overflow > 0 then
      local keep = {}
      for i = #a.ready, 1, -1 do
        local t = a.ready[i]
        local moved = false
        if overflow > 0 and WD.pr_producing(t.kind) then
          local best, best_spare = nil, 0
          for _, b in ipairs(st) do
            if b.f ~= a.f and b.present and (#builders == 0 or builder_set[b.f] or b.f == coordinator) then
              local spare = b.slots - b.desired
              if spare > best_spare then
                best, best_spare = b, spare
              end
            end
          end
          if best then
            WD.move(t.S, t.id, a.f, best.f, 'underfull', actor, idem, at)
            redis.call('XADD', 'friend:' .. best.f .. ':wake', 'MAXLEN', '~', 1000, '*',
              'kind', 'moved', 'id', t.id, 'sprint', t.S, 'from', a.f, 'reason', 'underfull', 'at', tostring(at))
            events[#events + 1] = 'MOVE ' .. t.id .. ' from=' .. a.f .. ' to=' .. best.f .. ' reason=underfull'
            best.ready[#best.ready + 1] = t
            best.desired = math.min(best.slots, best.working + best.starting + #best.ready)
            best.fillable = math.max(0, best.desired - best.working - best.starting)
            best.deficit = math.max(0, best.desired - best.working)
            overflow = overflow - 1
            moved = true
          end
        end
        if not moved then
          table.insert(keep, 1, t)
        end
      end
      a.ready = keep
      a.desired = math.min(a.slots, a.working + a.starting + #a.ready)
      a.deficit = math.max(0, a.desired - a.working)
      a.fillable = math.max(0, a.desired - a.working - a.starting)
    end
  end

  -- READ-BOUND: every named reader is at its effective slots or absent.
  local read_bound = 0
  if #readers > 0 then
    read_bound = 1
    local by = {}
    for _, s in ipairs(st) do by[s.f] = s end
    for _, r in ipairs(readers) do
      local s = by[r]
      if s and s.present and s.working + s.starting < s.slots then
        read_bound = 0
      end
    end
    if read_bound == 1 then
      events[#events + 1] = 'READ-BOUND readers=' .. #readers
    end
  end

  local rows = {}
  local fleet_working, fleet_desired = 0, 0
  for _, s in ipairs(st) do
    local wkey = 'friend:' .. s.f .. ':width'
    local prev_at = WD.num(redis.call('HGET', wkey, 'at'))
    local working_ms = WD.num(redis.call('HGET', wkey, 'working_ms'))
    local desired_ms = WD.num(redis.call('HGET', wkey, 'desired_ms'))
    if prev_at > 0 and at > prev_at then
      local dt = at - prev_at
      working_ms = working_ms + WD.num(redis.call('HGET', wkey, 'working')) * dt
      desired_ms = desired_ms + WD.num(redis.call('HGET', wkey, 'desired')) * dt
    end
    local idle = WD.idle(s.declared, s.slots, s.working, s.starting, s.fillable, s.blocked)
    redis.call('HSET', wkey,
      'slots', tostring(s.slots), 'declared', tostring(s.declared), 'cap', tostring(s.cap),
      'working', tostring(s.working), 'starting', tostring(s.starting),
      'ready_open', tostring(#s.ready), 'blocked', tostring(s.blocked), 'blocked_why', s.why,
      'desired', tostring(s.desired), 'deficit', tostring(s.deficit),
      'fillable', tostring(s.fillable), 'idle', idle,
      'stuck', tostring(s.stuck), 'under', tostring(s.under),
      'working_ms', tostring(working_ms), 'desired_ms', tostring(desired_ms),
      'at', tostring(at))
    redis.call('XADD', 'width:log', 'MAXLEN', '~', 200000, '*',
      'f', s.f, 'working', tostring(s.working), 'desired', tostring(s.desired), 'at', tostring(at))
    fleet_working = fleet_working + s.working
    fleet_desired = fleet_desired + s.desired

    -- The coordinator's wake carries what `nova-sprint fill` will print: the
    -- fillable ready ids, without PR-producing ones while READ-BOUND.
    if s.f == coordinator and s.present then
      local spawn = 0
      for _, t in ipairs(s.ready) do
        if read_bound == 0 or not WD.pr_producing(t.kind) then
          spawn = spawn + 1
        end
      end
      spawn = math.min(spawn, s.fillable)
      if spawn > 0 then
        events[#events + 1] = 'UNDERFULL ' .. s.f .. ' deficit=' .. s.deficit .. ' spawn=' .. spawn
        if redis.call('HGET', wkey, 'woke') ~= tostring(spawn) then
          redis.call('XADD', 'friend:' .. s.f .. ':wake', 'MAXLEN', '~', 1000, '*',
            'kind', 'underfull', 'deficit', tostring(s.deficit), 'spawn', tostring(spawn), 'at', tostring(at))
          redis.call('HSET', wkey, 'woke', tostring(spawn))
        end
      else
        redis.call('HDEL', wkey, 'woke')
      end
    end

    rows[#rows + 1] = {
      s.f, tostring(s.slots), tostring(s.cap), tostring(s.working), tostring(s.starting),
      tostring(#s.ready), tostring(s.desired), tostring(s.deficit), tostring(s.fillable), idle,
    }
  end
  redis.call('HSET', 'sprint:width', 'working', tostring(fleet_working),
    'desired', tostring(fleet_desired), 'read_bound', tostring(read_bound), 'at', tostring(at))
  return { rows, events }
end

-- ns_width_ready: read only. The ready ids a fill may start now for friend f,
-- in queue order, at most fillable (live), without PR-producing ones while
-- sprint:width read_bound=1. Returns { gen, fillable, S1, id1, kind1, brief1, ... }.
-- brief is the task's `brief` field, else its ref.
local function width_ready(keys, args)
  local f = args[1]
  local sprints = WD.open_sprints()
  local working = redis.call('ZCARD', 'friend:' .. f .. ':living')
  local starting = redis.call('ZCARD', 'friend:' .. f .. ':starting')
  local slots = WD.num(redis.call('HGET', 'friend:' .. f .. ':width', 'slots_cap'))
  local declared = WD.declared(f)
  if slots <= 0 or slots > declared then slots = declared end
  local ready = WD.queue(f, sprints)
  local fillable = math.max(0, math.min(slots, working + starting + #ready) - working - starting)
  local read_bound = redis.call('HGET', 'sprint:width', 'read_bound') == '1'
  local out = { redis.call('HGET', 'friend:' .. f .. ':width', 'gen') or '0', tostring(fillable) }
  local n = 0
  for _, t in ipairs(ready) do
    if n >= fillable then break end
    if not (read_bound and WD.pr_producing(t.kind)) then
      local key = 's:' .. t.S .. ':task:' .. t.id
      local brief = redis.call('HGET', key, 'brief') or ''
      if brief == '' then brief = redis.call('HGET', key, 'ref') or '' end
      out[#out + 1] = t.S
      out[#out + 1] = t.id
      out[#out + 1] = t.kind
      out[#out + 1] = brief
      n = n + 1
    end
  end
  return out
end

-- ns_width_reserve: the atomic reservation (Stella, #3071 comment 1): READY
-- (deps merged, read at head), owner (on f's queue), dedup (state open), slot
-- generation (the gen the caller read with ns_width_ready; a reservation by
-- anyone else since then refuses STALE) and a free effective slot. The task
-- becomes claimed (friend:<f>:starting); it becomes WORKING only on the
-- child's first beat (ns_task_beat), never here.
-- args = S, id, friend, gen, attempt, token, token_sha, actor, idem, fence.
-- fence empty: the friend reserving for its own loop. fence set: the
-- reconciler dealing a replacement (width.Duty); it must be the
-- lease:reconciler token (else FENCED, nothing written), and the same call
-- appends the spawn line to friend:<f>:wake (kind fill), so a claim the
-- reconciler makes always reaches the friend's harness.
local function width_reserve(keys, args)
  local S, id, f, gen = args[1], args[2], args[3], args[4]
  local attempt_arg, token, token_sha = tonumber(args[5]), args[6], args[7]
  local actor, idem, fence = args[8], args[9], args[10] or ''
  local key = 's:' .. S .. ':task:' .. id
  local wkey = 'friend:' .. f .. ':width'

  if fence ~= '' and not WD.holds(fence) then
    return WD.fenced()
  end
  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  if (redis.call('HGET', wkey, 'gen') or '0') ~= gen then
    return { 'STALE' }
  end
  if redis.call('HGET', 's:' .. S, 'status') ~= 'open' or
      redis.call('HGET', key, 'state') ~= 'open' or
      not redis.call('ZSCORE', 's:' .. S .. ':open:' .. f, id) then
    return { 'NONE' }
  end
  local ok, why = WD.ready(S, key)
  if not ok then
    return { 'NOTREADY', why }
  end
  if redis.call('SISMEMBER', 'friends', f) == 0 or
      redis.call('EXISTS', 'friend:' .. f .. ':beat') == 0 or
      redis.call('HGET', 'friend:' .. f .. ':desired', 'paused') == '1' then
    return { 'DOWN' }
  end
  local working = redis.call('ZCARD', 'friend:' .. f .. ':living')
  local starting = redis.call('ZCARD', 'friend:' .. f .. ':starting')
  local declared = WD.declared(f)
  local slots = WD.num(redis.call('HGET', wkey, 'slots_cap'))
  if slots <= 0 or slots > declared then slots = declared end
  if working + starting >= slots then
    return { 'FULL' }
  end
  local kind = redis.call('HGET', key, 'kind') or ''
  if redis.call('HGET', 'sprint:width', 'read_bound') == '1' and WD.pr_producing(kind) then
    return { 'READBOUND' }
  end

  local attempt = WD.num(redis.call('HGET', key, 'attempt')) + 1
  if attempt_arg ~= attempt or not string.match(token, '^%d+%.[0-9a-f]+$') or
      string.sub(token, 1, #tostring(attempt) + 1) ~= tostring(attempt) .. '.' or
      #token ~= #tostring(attempt) + 33 or
      not string.match(token_sha, '^[0-9a-f]+$') or #token_sha ~= 12 then
    return { 'RETRY' }
  end
  local at = WD.now_ms()
  redis.call('HSET', key, 'state', 'claimed', 'owner', f,
    'attempt', tostring(attempt), 'token', token, 'token_sha', token_sha, 'claimed_at', tostring(at))
  redis.call('ZREM', 's:' .. S .. ':ready', id)
  redis.call('ZREM', 's:' .. S .. ':open:' .. f, id)
  redis.call('SREM', 's:' .. S .. ':idx:task:open', id)
  redis.call('SADD', 's:' .. S .. ':idx:task:claimed', id)
  redis.call('ZADD', 'friend:' .. f .. ':starting', at, S .. '/' .. id .. '/' .. attempt)
  local next_gen = tostring(WD.num(gen) + 1)
  redis.call('HSET', wkey, 'gen', next_gen)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', 'task take', 'id', id, 'from', 'open', 'to', 'claimed',
    'attempt', tostring(attempt), 'token_sha', token_sha,
    'actor', actor or '', 'reason', 'width-reserve gen=' .. gen, 'evidence', '',
    'idem', idem or '', 'at', tostring(at))
  if fence ~= '' then
    local brief = redis.call('HGET', key, 'brief') or ''
    if brief == '' then brief = redis.call('HGET', key, 'ref') or '' end
    redis.call('XADD', 'friend:' .. f .. ':wake', 'MAXLEN', '~', 1000, '*',
      'kind', 'fill', 'id', id, 'sprint', S, 'brief', brief, 'attempt', tostring(attempt),
      'token', token, 'at', tostring(at))
  end
  return { 'CLAIMED', S, id, tostring(attempt), token, next_gen }
end

-- WD.id_after: stream id a is strictly after b ("ms-seq"; empty is before all).
function WD.id_after(a, b)
  if b == nil or b == '' then return a ~= nil and a ~= '' end
  local ams, aseq = string.match(a or '', '^(%d+)-(%d+)$')
  local bms, bseq = string.match(b, '^(%d+)-(%d+)$')
  if not ams then return false end
  if not bms then return true end
  ams, aseq, bms, bseq = tonumber(ams), tonumber(aseq), tonumber(bms), tonumber(bseq)
  return ams > bms or (ams == bms and aseq > bseq)
end

-- ns_width_cursor: the width duty's durable completion cursor (Stella's hold
-- 3 at 54755384 on #3086). args = fence, then S, id pairs. It refuses FENCED
-- unless fence is the lease:reconciler token, before any write; otherwise it
-- advances s:<S>:width:cursor to id, never backwards. The duty reads it at
-- the start of every pass, so a new instance resumes where the last one's
-- processed batch ended (never at the log tip) and a completion written
-- between a read and a restart is replayed. No cursor yet means the sprint's
-- log is read from its start (0-0: the log begins at the sprint's open).
local function width_cursor(keys, args)
  if not WD.holds(args[1]) then
    return WD.fenced()
  end
  local n = 0
  for i = 2, #args - 1, 2 do
    local key = 's:' .. args[i] .. ':width:cursor'
    if WD.id_after(args[i + 1], redis.call('GET', key) or '') then
      redis.call('SET', key, args[i + 1])
      n = n + 1
    end
  end
  return { 'OK', tostring(n) }
end

redis.register_function('ns_width_tick', width_tick)
redis.register_function{function_name = 'ns_width_ready', callback = width_ready, flags = {'no-writes'}}
redis.register_function('ns_width_reserve', width_reserve)
redis.register_function('ns_width_cursor', width_cursor)
