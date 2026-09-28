-- Control teardown (#3442): nova-sprint drain --control <C> removes every key
-- a control run left in the fleet store, found through the store's own index
-- sets and the control's registry, never by KEYS or SCAN.
--
-- A control run names everything it creates under its control id C
-- (control-<id>): its sprint is C, its benches, friends and machines are C or
-- C-<suffix>. The teardown, in one call:
--   1. every sprint in sprints under C, and C itself: its cards through the
--      card model's one purge (02_card_move.lua, NS.card.purge), its fixed
--      keys, its per-bench queues, then out of sprints;
--   2. every member of benches and friends under C: out of the registry set,
--      its fixed keys and card views deleted; the machine its desired hash
--      names is torn down too when that machine is under C;
--   3. each such machine: ceiling, budget, debits and one debit per debtor;
--   4. every key a control run registered in control:<C>:keys (RegisterKeys
--      in the card package), then the registry itself.
-- Nothing outside C is touched: a name is under C when it is C or starts
-- with C- (control-ab never tears down control-abc).
do
  local CARD = NS.card
  local CT_WHERE = { 'waiting', 'ready', 'working', 'done', 'parked', 'ok', 'fail' }
  -- (the old lease ledgers fold into <consumer>:cards:working, #3998: the
  -- CT_WHERE sweep below takes them)
  local CT_BENCH = { '', ':desired', ':beat', ':queue', ':working', ':done', ':state', ':live',
    ':owner', ':land', ':hold', ':ssh', ':reset', ':conform' }
  local CT_FRIEND = { '', ':desired', ':beat', ':queue', ':state', ':roles',
    ':serve', ':slots', ':down', ':fillstate', ':idle', ':last', ':log', ':wake', ':wakehealth',
    ':wakepath', ':waiting' }
  local CT_SPRINT = { '', ':pool', ':waiting', ':log', ':paused', ':drain:imported', ':ready', ':done',
    ':plan', ':policy', ':pitstop', ':backpressure', ':units', ':unresolved', ':superseded', ':routed',
    ':prcard', ':idem', ':landable', ':probes', ':prs', ':hold:events', ':holdpark', ':land:landed',
    ':land:open', ':land:queues', ':land:runs', ':reads:pending', ':idx:idem:pending', ':idx:pr:reading' }
  local CT_STATES = { 'queued', 'dealt', 'launched', 'running', 'reconcile-required', 'orphan-effect',
    'ended', 'harvested', 'refused', 'review-ready', 'land-ready', 'landed', 'superseded', 'done' }

  local function ct_under(C, name)
    return name == C or string.sub(name, 1, #C + 1) == C .. '-'
  end

  local function ct_del(dead, key)
    dead[#dead + 1] = key
  end

  -- consumer tears down one bench or friend under C and returns its machine.
  local function ct_consumer(dead, prefix, name, suffixes)
    local base = prefix .. name
    local machine = redis.call('HGET', base .. ':desired', 'machine')
    for _, s in ipairs(suffixes) do ct_del(dead, base .. s) end
    -- its sets under the current epoch and the legacy names (#4238)
    local e = CARD.epoch()
    for _, w in ipairs(CT_WHERE) do
      ct_del(dead, CARD.ckey(e, base, w))
      if e ~= 0 then ct_del(dead, CARD.ckey(0, base, w)) end
    end
    return machine

  end

  -- ns_control_teardown: keys = control:<C>:keys; args = C.
  -- Returns {OK, sprints, cards, benches, friends, machines, removed}; removed
  -- counts the keys deleted besides the card records the purge took.
  local function control_teardown(keys, args)
    local reg, C = keys[1], args[1]
    if type(C) ~= 'string' or not string.match(C, '^control%-[a-z0-9-]+$') or #C > 40 then
      return redis.error_reply('ns_control_teardown: a control id is control-<id>, [a-z0-9-]{1,40}')
    end
    if reg ~= 'control:' .. C .. ':keys' then
      return redis.error_reply('ns_control_teardown: key ' .. tostring(reg) .. ' is not control:<C>:keys')
    end
    local dead = {}
    local benches = redis.call('SMEMBERS', 'benches')
    benches[#benches + 1] = '_pool'

    local sprints, seen = {}, {}
    for _, S in ipairs(redis.call('SMEMBERS', 'sprints')) do
      if ct_under(C, S) then sprints[#sprints + 1] = S; seen[S] = true end
    end
    if not seen[C] then sprints[#sprints + 1] = C end
    local cards = 0
    for _, S in ipairs(sprints) do
      local n, err = CARD.purge(S)
      if not n then return redis.error_reply('ns_control_teardown: ' .. err) end
      cards = cards + n
      local base = 's:' .. S
      for _, s in ipairs(CT_SPRINT) do ct_del(dead, base .. s) end
      -- the dealer's lists under the current epoch (#4238): base is s:<S>
      local e = CARD.epoch()
      if e ~= 0 then
        for _, l in ipairs({ 'pool', 'waiting' }) do ct_del(dead, CARD.skey(e, string.sub(base, 3), l)) end
      end
      for _, st in ipairs(CT_STATES) do ct_del(dead, base .. ':idx:card:' .. st) end
      for _, b in ipairs(benches) do
        ct_del(dead, base .. ':bench:' .. b .. ':queue')
        ct_del(dead, base .. ':bench:' .. b .. ':ended')
      end
      redis.call('SREM', 'sprints', S)
    end

    local machines, mseen = {}, {}
    local function machine_of(m)
      if m and m ~= '' and ct_under(C, m) and not mseen[m] then
        mseen[m] = true
        machines[#machines + 1] = m
      end
    end
    local nb, nf = 0, 0
    for _, b in ipairs(benches) do
      if b ~= '_pool' and ct_under(C, b) then
        machine_of(ct_consumer(dead, 'bench:', b, CT_BENCH))
        redis.call('SREM', 'benches', b)
        nb = nb + 1
      end
    end
    for _, f in ipairs(redis.call('SMEMBERS', 'friends')) do
      if ct_under(C, f) then
        machine_of(ct_consumer(dead, 'friend:', f, CT_FRIEND))
        redis.call('SREM', 'friends', f)
        nf = nf + 1
      end
    end
    for _, m in ipairs(machines) do
      local base = 'machine:' .. m
      for _, d in ipairs(redis.call('SMEMBERS', base .. ':debits')) do ct_del(dead, base .. ':debit:' .. d) end
      ct_del(dead, base .. ':ceiling')
      ct_del(dead, base .. ':budget')
      ct_del(dead, base .. ':debits')
    end

    for _, k in ipairs(redis.call('SMEMBERS', reg)) do ct_del(dead, k) end
    ct_del(dead, reg)
    local removed = 0
    for _, k in ipairs(dead) do removed = removed + redis.call('DEL', k) end
    return { 'OK', tostring(#sprints), tostring(cards), tostring(nb), tostring(nf),
      tostring(#machines), tostring(removed) }
  end

  redis.register_function('ns_control_teardown', control_teardown)
end
