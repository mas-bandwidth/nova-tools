-- The reconciler pass's reads, each one call (Glenn 2026-09-27: "You
-- always need to batch redis. This is standard."). The store is 128 ms
-- from the Studio: a chain of dependent reads in Go was a chain of round
-- trips, and the refill duty made twelve a pass. The two functions here
-- read in the server what the Go pass read over the wire, in the same
-- order, and answer flat typed rows the Go side decodes with the same
-- conversions it applied before. Neither writes a card; ns_refill_wake
-- writes only what the consumer group needs (the group, the claim, the
-- read cursor), as the Go pass did.
--
-- No shebang: the loader prepends the library header; this file sorts after
-- 02_card_move.lua and reaches it through NS.
do
  local function str(v)
    if v == nil or v == false then return '' end
    return tostring(v)
  end

  local function tomap(flat)
    local m = {}
    for i = 1, #flat, 2 do m[flat[i]] = flat[i + 1] end
    return m
  end

  local function sorted(list)
    table.sort(list)
    return list
  end

  local function starts(s, prefix)
    return string.sub(s, 1, #prefix) == prefix
  end

  -- sprint_list(e, S, list): the sprint's dealer list (pool or waiting)
  -- under epoch e (ws.SprintListAt).
  local function sprint_list(e, S, list)
    if e == 0 then return 's:' .. S .. ':' .. list end
    return 's:' .. S .. ':' .. string.format('%d', e) .. ':' .. list
  end

  -- consumer_key(e, c, col): consumer c's set at col under epoch e
  -- (ws.ConsumerKeyAt).
  local function consumer_key(e, c, col)
    if e == 0 then return c .. ':cards:' .. col end
    return c .. ':' .. string.format('%d', e) .. ':cards:' .. col
  end

  -- is_ref: a DEPENDS-ON entry that names a repository PR (<owner/repo>#<n>),
  -- deal.parseRef; anything else is a card label.
  local function is_ref(entry)
    local i = nil
    local from = 1
    while true do
      local j = string.find(entry, '#', from, true)
      if not j then break end
      i, from = j, j + 1
    end
    if not i or i <= 1 then return false end
    if not string.find(string.sub(entry, 1, i - 1), '/', 1, true) then return false end
    local n = tonumber(string.sub(entry, i + 1))
    return n ~= nil and n > 0 and string.match(string.sub(entry, i + 1), '^%d+$') ~= nil
  end

  -- ns_deal_input: deal.RedisSource.Read in one call. Rows, flat:
  --   now <ms>
  --   max_sessions <text>                         (cfg:deal, when readable)
  --   bench <name> <host> <user> <state> <paused> <legs> <slots> <working>
  --         <ci> <ssh_state> <ssh_at> <role> <enrolled 1|0|?>
  --   sprint <name> <share> <bp_state> <bp_read_bound> <bp_present 1|0>
  --          <backpressure_missing> <pitstop 1|0>
  --   card <sprint> <pool|waiting> <label> <score> <state> <leg> <tier>
  --        <bench> <depends_on> <repo> <base> <wait_why> <priority> <avoid>
  --   dep <sprint/label> <state> <outcome> <repo> <base> <pr> <pushed_sha>
  -- The benches in name order; the sprints in sprint:order, open ones; a
  -- sprint's pool in score order then its waiting set in name order; the
  -- dependency cards in key order. Every value is the record's text; the
  -- Go side converts as it did.
  local function deal_input(keys, args)
    local t = redis.call('TIME')
    local out = { 'now', tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)) }
    local function emit(...)
      -- select, not ipairs: a nil field (an absent hash key) is a hole
      -- ipairs would stop at, and every row has a fixed width
      local n, t = select('#', ...), { ... }
      for i = 1, n do out[#out + 1] = str(t[i]) end
    end
    -- cfg:deal max_sessions is optional: unreadable (an ACL without cfg:*)
    -- leaves the default
    local ms = redis.pcall('HGET', 'cfg:deal', 'max_sessions')
    if type(ms) == 'string' then emit('max_sessions', ms) end
    local e = NS.task.epoch()
    for _, b in ipairs(sorted(redis.call('SMEMBERS', 'benches'))) do
      local d = tomap(redis.call('HGETALL', 'bench:' .. b .. ':desired'))
      local beat = tomap(redis.call('HGETALL', 'bench:' .. b .. ':beat'))
      local state = redis.call('HGET', 'bench:' .. b .. ':state', 'state')
      local ssh = tomap(redis.call('HGETALL', 'bench:' .. b .. ':ssh'))
      local working = redis.call('ZCARD', consumer_key(e, 'bench:' .. b, 'working'))
      -- enrolment (#3998): a seat whose ACL cannot read consumers deals as
      -- before, not enrolled, never refused
      local enrolled = redis.pcall('SISMEMBER', 'consumers', 'bench:' .. b)
      if type(enrolled) ~= 'number' then enrolled = '?' end
      emit('bench', b, beat.host, beat.user, state, d.paused, d.legs, d.slots, working, beat.ci,
        ssh.state, ssh.at, d.role, enrolled)
    end
    local open = {}
    for _, S in ipairs(redis.call('SMEMBERS', 'sprints')) do open[S] = true end
    local names = {}
    for _, S in ipairs(redis.call('ZRANGE', 'sprint:order', 0, -1)) do
      if open[S] then names[#names + 1] = S end
    end
    local deps, depkeys = {}, {}
    local function want_dep(S, entry)
      if entry == '' or entry == '-' or entry == 'none' or is_ref(entry) then return end
      local k = S .. '/' .. entry
      if not deps[k] then
        deps[k] = true
        depkeys[#depkeys + 1] = k
      end
    end
    local fields = { 'state', 'leg', 'tier', 'bench', 'depends_on', 'repo', 'base', 'wait_why', 'priority', 'avoid' }
    for _, S in ipairs(names) do
      local meta = tomap(redis.call('HGETALL', 's:' .. S))
      if meta.status == 'open' then
        local policy = tomap(redis.call('HGETALL', 's:' .. S .. ':policy'))
        local bp = redis.call('HGETALL', 's:' .. S .. ':backpressure')
        local bpm = tomap(bp)
        local stop = redis.call('EXISTS', 's:' .. S .. ':pitstop')
        emit('sprint', S, policy.share, bpm.state, bpm.read_bound, #bp > 0 and '1' or '0',
          policy.backpressure_missing, stop)
        local function card(list, label, score)
          local v = redis.call('HMGET', 's:' .. S .. ':card:' .. label, unpack(fields))
          if v[1] ~= 'queued' then return end
          emit('card', S, list, label, score, v[1], v[2], v[3], v[4], v[5], v[6], v[7], v[8], v[9], v[10])
          for entry in string.gmatch(str(v[5]), '[^,]+') do
            want_dep(S, (string.gsub(entry, '^%s+', ''):gsub('%s+$', '')))
          end
        end
        local pool = redis.call('ZRANGE', sprint_list(e, S, 'pool'), 0, -1, 'WITHSCORES')
        for i = 1, #pool, 2 do card('pool', pool[i], pool[i + 1]) end
        for _, label in ipairs(sorted(redis.call('SMEMBERS', sprint_list(e, S, 'waiting')))) do
          card('waiting', label, '0')
        end
      end
    end
    table.sort(depkeys)
    for _, k in ipairs(depkeys) do
      local slash = string.find(k, '/', 1, true)
      local S, label = string.sub(k, 1, slash - 1), string.sub(k, slash + 1)
      local v = redis.call('HMGET', 's:' .. S .. ':card:' .. label, 'state', 'outcome', 'repo', 'base', 'pr', 'pushed_sha')
      if v[1] then emit('dep', k, v[1], v[2], v[3], v[4], v[5], v[6]) end
    end
    return out
  end

  -- classify names what one stream event wakes (reconcile.Classify): an
  -- event by the reconciler's own actor, and any beat, wakes nothing; on
  -- cap:log an event about a friend is a route wake, any other a deal wake;
  -- on s:<S>:log a task or friend event is a route wake, a card or sprint
  -- event a deal wake.
  local function classify(stream, v, actor)
    local kind = str(v.kind)
    if str(v.actor) == actor or string.sub(kind, -5) == ' beat' or kind == 'beat' then return 'none' end
    if stream == 'cap:log' then
      if starts(str(v.consumer), 'friend:') or starts(str(v.target), 'friend:') then return 'route' end
      return 'deal'
    end
    if starts(kind, 'task ') or starts(kind, 'friend ') then return 'route' end
    if starts(kind, 'card ') or starts(kind, 'sprint ') then return 'deal' end
    return 'none'
  end

  -- ns_refill_wake consumer first actor count: the refill duty's wake read
  -- in one call: the streams to follow (cap:log, then every sprint's log in
  -- name order) with the reconciler group ensured on each at its end, the
  -- registered benches' states, on a first pass every entry a dead instance
  -- left pending claimed into this consumer, then this consumer's new
  -- entries on every stream, each classified. Rows, flat:
  --   streams <n> <name>...
  --   benches <n> (<name> <state>)...
  --   claimed <stream> <id> <deal|route|none>      (first pass)
  --   event <stream> <id> <deal|route|none>
  --   err <text>                                    (a group that could not be made)
  local function refill_wake(keys, args)
    local consumer, first, actor = args[1], args[2] == '1', args[3]
    local count = tonumber(args[4] or '') or 1000
    local out = {}
    local function emit(...)
      -- select, not ipairs: a nil field (an absent hash key) is a hole
      -- ipairs would stop at, and every row has a fixed width
      local n, t = select('#', ...), { ... }
      for i = 1, n do out[#out + 1] = str(t[i]) end
    end
    local streams = { 'cap:log' }
    for _, S in ipairs(sorted(redis.call('SMEMBERS', 'sprints'))) do streams[#streams + 1] = 's:' .. S .. ':log' end
    emit('streams', #streams)
    for _, s in ipairs(streams) do emit(s) end
    local benches = sorted(redis.call('SMEMBERS', 'benches'))
    emit('benches', #benches)
    for _, b in ipairs(benches) do emit(b, redis.call('HGET', 'bench:' .. b .. ':state', 'state')) end
    for _, s in ipairs(streams) do
      local made = redis.pcall('XGROUP', 'CREATE', s, 'reconciler', '$', 'MKSTREAM')
      if type(made) == 'table' and made.err and not string.find(made.err, 'BUSYGROUP', 1, true) then
        emit('err', 'group reconciler on ' .. s .. ': ' .. made.err)
      end
    end
    local function each(msgs, row, s)
      for _, m in ipairs(msgs) do
        emit(row, s, m[1], classify(s, tomap(m[2] or {}), actor))
      end
    end
    if first then
      for _, s in ipairs(streams) do
        local start = '0-0'
        while true do
          local r = redis.call('XAUTOCLAIM', s, 'reconciler', consumer, '0', start, 'COUNT', count)
          each(r[2] or {}, 'claimed', s)
          local nxt = str(r[1])
          if nxt == '' or nxt == '0-0' then break end
          start = nxt
        end
      end
    end
    local a = { 'GROUP', 'reconciler', consumer, 'COUNT', count, 'STREAMS' }
    for _, s in ipairs(streams) do a[#a + 1] = s end
    for _ in ipairs(streams) do a[#a + 1] = '>' end
    local r = redis.call('XREADGROUP', unpack(a))
    if r then
      for _, st in ipairs(r) do each(st[2] or {}, 'event', st[1]) end
    end
    return out
  end

  redis.register_function{ function_name = 'ns_deal_input', callback = deal_input, flags = { 'no-writes' } }
  redis.register_function('ns_refill_wake', refill_wake)
end
