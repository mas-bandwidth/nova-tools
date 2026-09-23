-- Capacity control transitions for spec 2.4. No shebang: loader.go prepends the
-- single library header. Every transition is one Redis Function call that reads
-- machine:<m>:ceiling, sums the desired slots of every registered friend and
-- bench on that machine, refuses a raise that would break the ceiling with exit
-- 2 CEILING <m> <sum>/<ceiling>, and otherwise writes the desired hash and one
-- cap:log receipt stamped with server TIME (spec #2756 2.1 rule 2, 2.2, 2.4).

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

  local key = desired_key(kind, name)
  local paused = redis.call('HGET', key, 'paused')
  if not paused then
    paused = '0'
  end
  local at = now_ms()
  if kind == 'bench' then
    redis.call('SADD', registry_set(kind), name)
  end
  redis.call('HSET', key,
    'slots', tostring(slots), 'machine', machine, 'paused', paused, 'at', tostring(at))
  receipt('capacity ' .. kind, kind .. ':' .. name, machine, slots, actor, idem, at)
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

redis.register_function('ns_capacity_desired', capacity_desired)
redis.register_function('ns_capacity_machine', capacity_machine)
