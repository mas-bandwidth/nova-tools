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

local function receipt(kind, target, machine, slots, actor, idem, at)
  redis.call('XADD', 'cap:log', '*',
    'kind', kind, 'target', target, 'machine', machine,
    'slots', tostring(slots), 'actor', actor or '', 'idem', idem or '',
    'at', tostring(at))
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
local function write_desired(kind, name, slots, machine, actor, idem, at)
  local key = desired_key(kind, name)
  local paused = redis.call('HGET', key, 'paused')
  if not paused then
    paused = '0'
  end
  redis.call('HSET', key,
    'slots', tostring(slots), 'machine', machine, 'paused', paused, 'at', tostring(at))
  receipt('capacity ' .. kind, kind .. ':' .. name, machine, slots, actor, idem, at)
end

-- capacity_desired: set friend:<f>:desired or bench:<b>:desired under the
-- machine ceiling. args = kind, name, slots, machine, actor, idem.
local function capacity_desired(keys, args)
  local kind, name = args[1], args[2]
  local slots, machine = tonumber(args[3]), args[4]
  local actor, idem = args[5], args[6]

  if kind ~= 'friend' and kind ~= 'bench' then
    return { 'INVALID', machine, '0', '0' }
  end
  if kind == 'friend' and redis.call('SISMEMBER', 'friends', name) == 0 then
    return { 'UNREGISTERED', machine, '0', '0' }
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

  local at = now_ms()
  if kind == 'bench' then
    redis.call('SADD', registry_set(kind), name)
  end
  write_desired(kind, name, slots, machine, actor, idem, at)
  return { 'SET', machine, tostring(sum), tostring(ceiling) }
end

-- capacity_machine: set machine:<m>:ceiling. Refuses a ceiling below the slots
-- already desired on the machine so config can never break the invariant.
-- args = machine, slots, cores, mem_gb, actor, idem.
local function capacity_machine(keys, args)
  local machine, slots = args[1], tonumber(args[2])
  local cores, mem_gb = args[3], args[4]
  local actor, idem = args[5], args[6]

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
-- ci_reruns, readers, absent_after, n, then kind, name, machine, slots per row.
local function sprint_plan(keys, args)
  -- 1. Authority: only a seat whose ACL may HSET authz:sprint-plan applies a
  -- plan. The key is never written; it names the right.
  if not redis.acl_check_cmd('HSET', 'authz:sprint-plan', 'f', 'v') then
    return redis.error_reply('NOAUTHZ sprint-plan')
  end

  -- 2. Check.
  local s, sha, body, applied_by, routes_sha = args[1], args[2], args[3], args[4], args[5]
  local n = tonumber(args[10])
  if not s or s == '' or not sha or sha == '' or not n or #args ~= 10 + 4 * n then
    return { 'INVALID', 'argv' }
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
    write_desired(r.kind, r.name, r.slots, r.machine, actor, idem, at)
  end
  redis.call('HSET', policy_key,
    'backpressure_missing', args[6], 'ci_reruns', args[7], 'readers', args[8],
    'absent_after', args[9], 'at', tostring(at))
  redis.call('HSET', plan_key,
    'version', '1', 'sha', sha, 'body', body, 'rows', tostring(n),
    'routes_sha', routes_sha, 'applied_at', tostring(at), 'applied_by', applied_by)
  return { 'APPLIED', sha, tostring(n) }
end

redis.register_function('ns_capacity_desired', capacity_desired)
redis.register_function('ns_capacity_machine', capacity_machine)
redis.register_function('ns_sprint_plan', sprint_plan)
