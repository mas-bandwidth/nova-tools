-- Capacity control transitions for spec 2.4. No shebang: loader.go prepends the
-- single library header. Every transition is one Redis Function call that reads
-- machine:<m>:ceiling, sums the desired slots of every registered friend and
-- bench on that machine, refuses a raise that would break the ceiling with exit
-- 2 CEILING <m> <sum>/<ceiling>, and otherwise writes the desired hash and one
-- cap:log receipt stamped with server TIME (spec #2756 2.1 rule 2, 2.2, 2.4).
-- ns_sprint_plan (#2380) applies a whole sprint plan the same way: every row's
-- desired hash through the one write_desired path, under a multi-row ceiling
-- check, after an ACL authority check and before any write.

local function now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function receipt(kind, target, machine, slots, actor, idem, at, legacy)
  redis.call('XADD', 'cap:log', '*',
    'kind', kind, 'target', target, 'machine', machine,
    'slots', tostring(slots), 'actor', actor or '', 'idem', idem or '',
    'legacy', legacy or '', 'at', tostring(at))
end

local function desired_key(kind, name)
  if kind == 'bench' then
    return 'bench:' .. name .. ':desired'
  end
  return 'friend:' .. name .. ':desired'
end

local function registry_set(kind)
  if kind == 'bench' then
    return 'benches'
  end
  return 'friends'
end

-- add_consumer adds one consumer's desired slots to total when its machine is
-- m. The requesting consumer is substituted: it counts as req_slots on machine
-- m whatever its stored hash says, so a move replaces the old contribution
-- instead of adding to it.
local function add_consumer(total, kind, name, m, req_kind, req_name, req_slots)
  if kind == req_kind and name == req_name then
    return total + req_slots
  end
  local key = desired_key(kind, name)
  if redis.call('HGET', key, 'machine') == m then
    return total + tonumber(redis.call('HGET', key, 'slots') or '0')
  end
  return total
end

-- machine_sum is the prospective sum of desired slots over every friend and
-- bench whose machine is m, with the requester counted even before it is
-- registered. It is the same rule Go's capacity.Evaluate applies; running it
-- here makes the guard and the write atomic.
local function machine_sum(m, req_kind, req_name, req_slots)
  local total = 0
  local counted = false
  for _, name in ipairs(redis.call('SMEMBERS', 'friends')) do
    if req_kind == 'friend' and name == req_name then
      counted = true
    end
    total = add_consumer(total, 'friend', name, m, req_kind, req_name, req_slots)
  end
  for _, name in ipairs(redis.call('SMEMBERS', 'benches')) do
    if req_kind == 'bench' and name == req_name then
      counted = true
    end
    total = add_consumer(total, 'bench', name, m, req_kind, req_name, req_slots)
  end
  if not counted and req_name ~= '' and (req_kind == 'friend' or req_kind == 'bench') then
    total = add_consumer(total, req_kind, req_name, m, req_kind, req_name, req_slots)
  end
  return total
end

-- write_desired is the one write path of a desired hash (#2380 rev 4): it
-- keeps paused (default 0), writes slots, machine, paused and at, and adds one
-- cap:log receipt. capacity_desired and ns_sprint_plan both call it. It does no
-- ceiling check and registers no name: each caller has already checked
-- everything, so a write phase built from it cannot refuse part way.
local function write_desired(kind, name, slots, machine, actor, idem, at, legacy, set_paused)
  local key = desired_key(kind, name)
  local paused = redis.call('HGET', key, 'paused')
  if not paused then
    paused = '0'
  end
  -- #3206 PR A: capacity_desired's optional paused arg; nil keeps the stored
  -- value, as every other caller does.
  if set_paused == '0' or set_paused == '1' then
    paused = set_paused
  end
  redis.call('HSET', key,
    'slots', tostring(slots), 'machine', machine, 'paused', paused, 'at', tostring(at))
  receipt('capacity ' .. kind, kind .. ':' .. name, machine, slots, actor, idem, at, legacy)
end

-- capacity_desired: set friend:<f>:desired or bench:<b>:desired under the
-- machine ceiling. args = kind, name, slots, machine, actor, idem, and (#3206
-- rev 4 PR A, both optional so six-arg callers are unchanged) paused ('' keeps
-- the stored value, '0' or '1' sets it) and register ('1' is accepted and
-- implied: since #2934 every write adds the name to its registry; no beat is
-- written). The same slots, machine
-- and paused as stored return SAME and write nothing.
local function capacity_desired(keys, args)
  local kind, name = args[1], args[2]
  local slots, machine = tonumber(args[3]), args[4]
  local actor, idem = args[5], args[6]
  local set_paused = args[7] or ''

  if kind ~= 'friend' and kind ~= 'bench' then
    return { 'INVALID', machine, '0', '0' }
  end
  if set_paused ~= '' and set_paused ~= '0' and set_paused ~= '1' then
    return { 'INVALID', machine, '0', '0' }
  end
  if not slots or slots < 0 then
    return { 'INVALID', machine, '0', '0' }
  end
  local ceiling_text = redis.call('HGET', 'machine:' .. machine .. ':ceiling', 'slots')
  if not ceiling_text then
    return { 'NOCEILING', machine, '0', '0' }
  end
  local ceiling = tonumber(ceiling_text)

  local sum = machine_sum(machine, kind, name, slots)
  if sum > ceiling then
    return { 'CEILING', machine, tostring(sum), tostring(ceiling) }
  end

  local key = desired_key(kind, name)
  local stored = redis.call('HMGET', key, 'slots', 'machine', 'paused')
  local want_paused = set_paused
  if want_paused == '' then
    want_paused = stored[3] or '0'
  end
  if stored[1] == tostring(slots) and stored[2] == machine and (stored[3] or '0') == want_paused and
      redis.call('SISMEMBER', registry_set(kind), name) == 1 then
    return { 'SAME', machine, tostring(sum), tostring(ceiling) }
  end

  local at = now_ms()
  local legacy = ''
  if kind == 'friend' then
    legacy = redis.call('GET', 'friend:' .. name .. ':slots') or ''
  end
  redis.call('SADD', registry_set(kind), name)
  write_desired(kind, name, slots, machine, actor, idem, at, legacy, set_paused)
  if kind == 'friend' then
    redis.call('DEL', 'friend:' .. name .. ':slots')
  end
  return { 'SET', machine, tostring(sum), tostring(ceiling) }
end

-- capacity_machine: set machine:<m>:ceiling and machine:<m>:budget (spec 5.1).
-- Refuses a ceiling below the slots already desired on the machine so config
-- can never break the invariant.
-- args = machine, slots, cores, mem_gb, actor, idem, cpu_milli, mem_mb.
local function capacity_machine(keys, args)
  local machine, slots = args[1], tonumber(args[2])
  local cores, mem_gb = args[3], args[4]
  local actor, idem = args[5], args[6]
  local cpu_milli = tonumber(args[7] or '')
  local mem_mb = tonumber(args[8] or '')

  if not slots or slots < 0 then
    return { 'INVALID', machine, '0', '0' }
  end
  local sum = machine_sum(machine, '', '', 0)
  if slots < sum then
    return { 'CEILING', machine, tostring(sum), tostring(slots) }
  end

  local at = now_ms()
  local key = 'machine:' .. machine .. ':ceiling'
  redis.call('HSET', key, 'slots', tostring(slots), 'at', tostring(at))
  if cores ~= '' then redis.call('HSET', key, 'cores', cores) end
  if mem_gb ~= '' then redis.call('HSET', key, 'mem_gb', mem_gb) end

  if not cpu_milli and cores ~= '' and tonumber(cores) and tonumber(cores) > 0 then
    cpu_milli = math.floor(tonumber(cores) * 1000 * 0.90)
  end
  if not mem_mb and mem_gb ~= '' and tonumber(mem_gb) and tonumber(mem_gb) > 0 then
    mem_mb = math.floor(tonumber(mem_gb) * 1024 * 0.90)
  end
  if cpu_milli or mem_mb then
    local bkey = 'machine:' .. machine .. ':budget'
    if cpu_milli then redis.call('HSET', bkey, 'cpu_milli', tostring(cpu_milli)) end
    if mem_mb then redis.call('HSET', bkey, 'mem_mb', tostring(mem_mb)) end
    redis.call('HSET', bkey, 'at', tostring(at))
  end

  receipt('capacity machine', machine, machine, slots, actor, idem, at)
  return { 'SET', machine, tostring(sum), tostring(slots) }
end

-- key_type is TYPE as a plain string ('none' for an absent key).
local function key_type(key)
  return redis.call('TYPE', key)['ok']
end

-- plan_machine_sum is the ceiling rule of a sprint plan (#2380 rev 4): on
-- machine m, the plan slots of every plan row whose machine is m, plus the
-- stored slots of every registered consumer outside the plan whose stored
-- machine is m (outside, summed once per call of ns_sprint_plan). A consumer
-- the plan moves off m no longer counts on m. Every plan row is substituted
-- at once, so a transfer between two consumers is never refused by the order
-- of its rows.
local function plan_machine_sum(m, rows, outside)
  local total = outside[m] or 0
  for _, r in ipairs(rows) do
    if r.machine == m then
      total = total + r.slots
    end
  end
  return total
end

-- sprint_plan applies one validated sprint plan (#2380 rev 4) in three phases:
-- authority, then every check, then every write. Redis does not undo a
-- script's earlier writes when it errors, so no write happens until every
-- check has passed; the write phase has no check that can refuse.
-- args = sprint, sha, body, applied_by, routes_sha, backpressure_missing,
-- ci_reruns, readers, absent_after, n, then kind, name, machine, slots per row,
-- then optionally fix_to and release_reader (#3798), both registered friends.
local function sprint_plan(keys, args)
  -- 1. Authority: only a seat whose ACL may HSET authz:sprint-plan applies a
  -- plan. The key is never written; it names the right.
  if not redis.acl_check_cmd('HSET', 'authz:sprint-plan', 'f', 'v') then
    return redis.error_reply('NOAUTHZ sprint-plan')
  end

  -- 2. Check.
  local s, sha, body, applied_by, routes_sha = args[1], args[2], args[3], args[4], args[5]
  local n = tonumber(args[10])
  if not s or s == '' or not sha or sha == '' or not n or (#args ~= 10 + 4 * n and #args ~= 12 + 4 * n) then
    return { 'INVALID', 'argv' }
  end
  local fix_to, release_reader = args[11 + 4 * n], args[12 + 4 * n]
  for _, f in ipairs({ fix_to, release_reader }) do
    if f == '' then
      return { 'INVALID', 'hold policy' }
    end
    if redis.call('SISMEMBER', 'friends', f) == 0 then
      return { 'UNREGISTERED', 'friend:' .. f }
    end
  end
  local plan_key = 's:' .. s .. ':plan'
  local policy_key = 's:' .. s .. ':policy'
  for _, key in ipairs({ plan_key, policy_key }) do
    local t = key_type(key)
    if t ~= 'hash' and t ~= 'none' then
      return { 'WRONGTYPE', key }
    end
  end
  if redis.call('HGET', plan_key, 'sha') == sha then
    return { 'UNCHANGED', sha }
  end

  local rows, in_plan, machines = {}, {}, {}
  for i = 1, n do
    local at = 10 + (i - 1) * 4
    local r = { kind = args[at + 1], name = args[at + 2], machine = args[at + 3], slots = tonumber(args[at + 4]) }
    if (r.kind ~= 'bench' and r.kind ~= 'friend') or r.name == '' or r.machine == '' or not r.slots or r.slots < 0 then
      return { 'INVALID', 'row ' .. i }
    end
    local id = r.kind .. ':' .. r.name
    if in_plan[id] then
      return { 'INVALID', 'repeated ' .. id }
    end
    if redis.call('SISMEMBER', registry_set(r.kind), r.name) == 0 then
      return { 'UNREGISTERED', id }
    end
    local key = desired_key(r.kind, r.name)
    local t = key_type(key)
    if t ~= 'hash' and t ~= 'none' then
      return { 'WRONGTYPE', key }
    end
    if t == 'hash' then
      local stored = redis.call('HGET', key, 'machine')
      if stored and machines[stored] == nil then
        machines[stored] = false
      end
    end
    in_plan[id] = true
    machines[r.machine] = true
    rows[#rows + 1] = r
  end

  local outside = {}
  for _, kind in ipairs({ 'friend', 'bench' }) do
    for _, name in ipairs(redis.call('SMEMBERS', registry_set(kind))) do
      if not in_plan[kind .. ':' .. name] then
        local key = desired_key(kind, name)
        local t = key_type(key)
        if t ~= 'hash' and t ~= 'none' then
          return { 'WRONGTYPE', key }
        end
        local v = redis.call('HMGET', key, 'machine', 'slots')
        if v[1] then
          outside[v[1]] = (outside[v[1]] or 0) + (tonumber(v[2] or '0') or 0)
        end
      end
    end
  end

  -- One sum per machine named by the plan (machines[m] true, ceiling
  -- required) or by a plan consumer's stored machine (false, checked when it
  -- has a ceiling).
  local names = {}
  for m in pairs(machines) do
    names[#names + 1] = m
  end
  table.sort(names)
  for _, m in ipairs(names) do
    local ceiling_key = 'machine:' .. m .. ':ceiling'
    local t = key_type(ceiling_key)
    if t ~= 'hash' and t ~= 'none' then
      return { 'WRONGTYPE', ceiling_key }
    end
    local ceiling = nil
    if t == 'hash' then
      ceiling = tonumber(redis.call('HGET', ceiling_key, 'slots') or '')
    end
    if not ceiling then
      if machines[m] then
        return { 'NOCEILING', m }
      end
    else
      local sum = plan_machine_sum(m, rows, outside)
      if sum > ceiling then
        return { 'CEILING', m, tostring(sum), tostring(ceiling) }
      end
    end
  end

  -- 3. Write. Every row, the policy and the plan record share one server TIME.
  local at = now_ms()
  local actor = 'plan:' .. s
  local idem = string.sub(sha, 1, 12)
  for _, r in ipairs(rows) do
    write_desired(r.kind, r.name, r.slots, r.machine, actor, idem, at, '')
  end
  redis.call('HSET', policy_key,
    'backpressure_missing', args[6], 'ci_reruns', args[7], 'readers', args[8],
    'absent_after', args[9], 'at', tostring(at))
  if fix_to then
    redis.call('HSET', policy_key, 'fix_to', fix_to, 'release_reader', release_reader)
  end
  redis.call('HSET', plan_key,
    'version', '1', 'sha', sha, 'body', body, 'rows', tostring(n),
    'routes_sha', routes_sha, 'applied_at', tostring(at), 'applied_by', applied_by)
  return { 'APPLIED', sha, tostring(n) }
end

-- capacity_consumers returns one flat snapshot of both registries and their
-- desired hashes. Go invokes it as the sole command in one pipeline so the
-- reader makes one round trip and no per-consumer calls.
local function capacity_consumers(keys, args)
  local out = {}
  for _, kind in ipairs({ 'friend', 'bench' }) do
    for _, name in ipairs(redis.call('SMEMBERS', registry_set(kind))) do
      local key = desired_key(kind, name)
      out[#out + 1] = kind
      out[#out + 1] = name
      out[#out + 1] = redis.call('HGET', key, 'slots') or ''
      out[#out + 1] = redis.call('HGET', key, 'machine') or ''
    end
  end
  return out
end

-- cap_budget_set sets machine:<m>:budget (cpu_milli, mem_mb).
-- args = machine, cpu_milli, mem_mb, actor, idem.
local function cap_budget_set(keys, args)
  local machine = args[1]
  local cpu_milli = tonumber(args[2] or '0') or 0
  local mem_mb = tonumber(args[3] or '0') or 0
  local actor = args[4] or ''
  local idem = args[5] or ''
  local at = now_ms()
  local bkey = 'machine:' .. machine .. ':budget'
  redis.call('HSET', bkey, 'cpu_milli', tostring(cpu_milli), 'mem_mb', tostring(mem_mb), 'at', tostring(at))
  receipt('capacity budget', machine, machine, cpu_milli, actor, idem, at)
  return { 'SET', machine, tostring(cpu_milli), tostring(mem_mb) }
end

-- cap_budget_take debits machine:<m>:budget for one consumer atomically (spec 5.1).
-- Refuses with NOBUDGET when live and quarantined debits plus request exceed the budget.
-- args = machine, consumer, cpu_milli, mem_mb, ttl_ms, pgid, kind.
local function cap_budget_take(keys, args)
  local machine = args[1]
  local consumer = args[2]
  local req_cpu = tonumber(args[3] or '0') or 0
  local req_mem = tonumber(args[4] or '0') or 0
  local ttl_ms = tonumber(args[5] or '30000') or 30000
  if ttl_ms <= 0 then ttl_ms = 30000 end
  local pgid = args[6] or ''
  local kind = args[7] or ''

  local bkey = 'machine:' .. machine .. ':budget'
  local budget_cpu = tonumber(redis.call('HGET', bkey, 'cpu_milli') or '')
  local budget_mem = tonumber(redis.call('HGET', bkey, 'mem_mb') or '')
  if not budget_cpu or not budget_mem then
    local ckey = 'machine:' .. machine .. ':ceiling'
    local cores = tonumber(redis.call('HGET', ckey, 'cores') or '')
    local mem_gb = tonumber(redis.call('HGET', ckey, 'mem_gb') or '')
    if cores and cores > 0 then budget_cpu = math.floor(cores * 1000 * 0.90) end
    if mem_gb and mem_gb > 0 then budget_mem = math.floor(mem_gb * 1024 * 0.90) end
  end
  if not budget_cpu or not budget_mem then
    return { 'NOBUDGET', machine, '0', '0', '0', '0' }
  end

  local debits_key = 'machine:' .. machine .. ':debits'
  local consumers = redis.call('SMEMBERS', debits_key)
  local used_cpu = 0
  local used_mem = 0
  local now = now_ms()
  for _, c in ipairs(consumers) do
    local dkey = 'machine:' .. machine .. ':debit:' .. c
    if redis.call('EXISTS', dkey) == 1 then
      local d_cpu = tonumber(redis.call('HGET', dkey, 'cpu_milli') or '0') or 0
      local d_mem = tonumber(redis.call('HGET', dkey, 'mem_mb') or '0') or 0
      local renew_at = tonumber(redis.call('HGET', dkey, 'renew_at') or '0') or 0
      local state = redis.call('HGET', dkey, 'state')
      if renew_at > 0 and now > renew_at and state ~= 'quarantined' then
        redis.call('HSET', dkey, 'state', 'quarantined')
        state = 'quarantined'
      end
      if c ~= consumer then
        used_cpu = used_cpu + d_cpu
        used_mem = used_mem + d_mem
      end
    else
      redis.call('SREM', debits_key, c)
    end
  end

  if (used_cpu + req_cpu > budget_cpu) or (used_mem + req_mem > budget_mem) then
    return { 'NOBUDGET', machine, tostring(used_cpu + req_cpu), tostring(budget_cpu), tostring(used_mem + req_mem), tostring(budget_mem) }
  end

  local dkey = 'machine:' .. machine .. ':debit:' .. consumer
  local renew_time = now + ttl_ms
  redis.call('SADD', debits_key, consumer)
  redis.call('HSET', dkey,
    'cpu_milli', tostring(req_cpu),
    'mem_mb', tostring(req_mem),
    'pgid', tostring(pgid),
    'renew_at', tostring(renew_time),
    'at', tostring(now),
    'state', 'live',
    'machine', machine,
    'consumer', consumer,
    'kind', kind)
  redis.call('SET', 'debit:machine:' .. consumer, machine)
  receipt('budget take', consumer, machine, req_cpu, kind, '', now)
  return { 'OK', machine, tostring(used_cpu + req_cpu), tostring(budget_cpu), tostring(used_mem + req_mem), tostring(budget_mem) }
end

-- cap_budget_renew updates renew_at and pgid for an active debit (spec 5.1).
-- args = machine, consumer, pgid, ttl_ms OR consumer, pgid, ttl_ms.
local function cap_budget_renew(keys, args)
  local machine, consumer, pgid, ttl_ms
  if #args >= 2 and redis.call('EXISTS', 'machine:' .. args[1] .. ':debit:' .. args[2]) == 1 then
    machine = args[1]
    consumer = args[2]
    pgid = args[3] or ''
    ttl_ms = tonumber(args[4] or '30000') or 30000
  else
    consumer = args[1]
    pgid = args[2] or ''
    ttl_ms = tonumber(args[3] or '30000') or 30000
    machine = redis.call('GET', 'debit:machine:' .. consumer)
  end
  if not machine or machine == '' then
    return { 'NOTFOUND', consumer }
  end
  local dkey = 'machine:' .. machine .. ':debit:' .. consumer
  if redis.call('EXISTS', dkey) == 0 then
    return { 'NOTFOUND', consumer }
  end
  local now = now_ms()
  if ttl_ms <= 0 then ttl_ms = 30000 end
  local renew_time = now + ttl_ms
  redis.call('HSET', dkey, 'renew_at', tostring(renew_time), 'state', 'live')
  if pgid ~= '' then redis.call('HSET', dkey, 'pgid', tostring(pgid)) end
  return { 'OK', consumer, tostring(renew_time) }
end

-- cap_budget_give returns capacity for a completed or reaped debit (spec 5.1).
-- A quarantined debit with an active process group requires confirmed ESRCH.
-- args = consumer, pgid, confirmed OR machine, consumer, pgid, confirmed.
local function cap_budget_give(keys, args)
  local machine, consumer, pgid, confirmed
  if #args >= 2 and redis.call('EXISTS', 'machine:' .. args[1] .. ':debit:' .. args[2]) == 1 then
    machine = args[1]
    consumer = args[2]
    pgid = args[3] or ''
    confirmed = args[4] or ''
  else
    consumer = args[1]
    pgid = args[2] or ''
    confirmed = args[3] or ''
    machine = redis.call('GET', 'debit:machine:' .. consumer)
  end
  if not machine or machine == '' then
    return { 'NOTFOUND', consumer }
  end
  local dkey = 'machine:' .. machine .. ':debit:' .. consumer
  if redis.call('EXISTS', dkey) == 0 then
    return { 'NOTFOUND', consumer }
  end
  local state = redis.call('HGET', dkey, 'state')
  local stored_pgid = redis.call('HGET', dkey, 'pgid') or ''
  local renew_at = tonumber(redis.call('HGET', dkey, 'renew_at') or '0') or 0
  local now = now_ms()
  if renew_at > 0 and now > renew_at then state = 'quarantined' end

  if state == 'quarantined' and stored_pgid ~= '' and stored_pgid ~= '0' then
    local is_confirmed = (confirmed == '1' or confirmed == 'true' or confirmed == 'confirmed' or confirmed == 'ESRCH')
    if not is_confirmed then
      return { 'STILLALIVE', consumer, stored_pgid }
    end
  end

  local d_cpu = tonumber(redis.call('HGET', dkey, 'cpu_milli') or '0') or 0
  redis.call('DEL', dkey)
  redis.call('SREM', 'machine:' .. machine .. ':debits', consumer)
  redis.call('DEL', 'debit:machine:' .. consumer)
  receipt('budget give', consumer, machine, d_cpu, '', '', now)
  return { 'OK', consumer }
end

-- cap_budget_debits returns debits on machine m as colon-separated strings.
local function cap_budget_debits(keys, args)
  local machine = args[1]
  local debits_key = 'machine:' .. machine .. ':debits'
  local consumers = redis.call('SMEMBERS', debits_key)
  local result = {}
  local now = now_ms()
  for _, c in ipairs(consumers) do
    local dkey = 'machine:' .. machine .. ':debit:' .. c
    if redis.call('EXISTS', dkey) == 1 then
      local d_cpu = redis.call('HGET', dkey, 'cpu_milli') or '0'
      local d_mem = redis.call('HGET', dkey, 'mem_mb') or '0'
      local pgid = redis.call('HGET', dkey, 'pgid') or ''
      local renew_at = redis.call('HGET', dkey, 'renew_at') or '0'
      local state = redis.call('HGET', dkey, 'state') or 'live'
      local kind = redis.call('HGET', dkey, 'kind') or ''
      if tonumber(renew_at) > 0 and now > tonumber(renew_at) then
        state = 'quarantined'
      end
      table.insert(result, c .. ':' .. d_cpu .. ':' .. d_mem .. ':' .. pgid .. ':' .. renew_at .. ':' .. state .. ':' .. kind)
    else
      redis.call('SREM', debits_key, c)
    end
  end
  return result
end

redis.register_function('ns_capacity_desired', capacity_desired)
redis.register_function('ns_capacity_machine', capacity_machine)
redis.register_function('ns_capacity_consumers', capacity_consumers)
redis.register_function('ns_budget_set', cap_budget_set)
redis.register_function('ns_budget_take', cap_budget_take)
redis.register_function('ns_budget_renew', cap_budget_renew)
redis.register_function('ns_budget_give', cap_budget_give)
redis.register_function('ns_budget_debits', cap_budget_debits)
redis.register_function('ns_sprint_plan', sprint_plan)

-- land.lua (a later file, in its own do-block) debits and returns lander
-- capacity through these two; NS is the only way across file blocks.
NS.capacity = { cap_budget_take = cap_budget_take, cap_budget_give = cap_budget_give }
