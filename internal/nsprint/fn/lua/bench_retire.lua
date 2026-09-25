-- Bench retire (nova-tools#3645): take one bench out of the fleet in one
-- call. Before any write it refuses while the bench holds a lease (a card in
-- its working set, a starting or living entry, or a harvest lease): those
-- are stopped by `nova-sprint bench reset` first. Then every card whose
-- pointer still names the bench moves through the one card move
-- (02_card_move.lua, NS.card): a waiting, ready or parked card returns to the
-- pool with its pin cleared, and a done card is re-pointed to the _retired
-- bench with retired_bench=<b> on its record, so no card disappears and no
-- set is left naming a bench that is gone. Then the bench's rows (the key
-- suffixes the caller passes: benchretire.Rows, which a test holds to every
-- bench key the tree writes) and ci:nomirror:<b> are deleted, the bench
-- leaves the benches set, and one bench-retire receipt goes to cap:log.
-- Idempotent: a bench already gone answers ALREADY and writes nothing.
-- Wrapped in do/end so its helpers stay out of the library main chunk's
-- 200-local limit (the same shape as bench_reset.lua).
do
  local CARD = NS.card
  local RETIRE_PLACES = { 'waiting', 'ready', 'parked', 'done' }

  local function retire_now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  -- ns_bench_retire(bench, actor, why, row suffix...) -> LEASED <what> |
  -- ALREADY | REFUSED <card> <why> requeued repointed | RETIRED requeued
  -- repointed deleted registered
  local function bench_retire(keys, args)
    local bench, actor, why = args[1], args[2], args[3]
    if not bench or not string.match(bench, '^[A-Za-z0-9][A-Za-z0-9._-]*$') then
      return redis.error_reply('ns_bench_retire: bench must be a bench name')
    end
    if not actor or actor == '' or not why or why == '' then
      return redis.error_reply('ns_bench_retire: actor and why are required')
    end
    for i = 4, #args do
      if args[i] ~= '' and not string.match(args[i], '^:[a-z_:]+$') then
        return redis.error_reply('ns_bench_retire: a row suffix is empty or :name, got ' .. args[i])
      end
    end
    local pre = 'bench:' .. bench

    -- Leases first, before any write.
    local held = {}
    for _, k in ipairs({ pre .. ':cards:working', pre .. ':starting', pre .. ':living' }) do
      local n = redis.call('ZCARD', k)
      if n > 0 then held[#held + 1] = k .. '=' .. n end
    end
    if redis.call('EXISTS', 'lease:harvest:' .. bench) == 1 then held[#held + 1] = 'lease:harvest:' .. bench end
    if #held > 0 then return { 'LEASED', table.concat(held, ' ') } end

    local registered = redis.call('SISMEMBER', 'benches', bench)
    local present = redis.call('EXISTS', 'ci:nomirror:' .. bench)
    for i = 4, #args do present = present + redis.call('EXISTS', pre .. args[i]) end
    for _, w in ipairs(RETIRE_PLACES) do present = present + redis.call('EXISTS', pre .. ':cards:' .. w) end
    if registered == 0 and present == 0 then return { 'ALREADY' } end

    -- Every card still pointing here moves through the one card move.
    local requeued, repointed = 0, 0
    for _, w in ipairs(RETIRE_PLACES) do
      for _, id in ipairs(redis.call('ZRANGE', pre .. ':cards:' .. w, 0, -1)) do
        local o = { bench = '', by = actor, why = 'bench-retire' }
        if w == 'done' then
          o.bench = '_retired'
          o.fields = { 'retired_bench', bench }
        elseif redis.call('HGET', id, 'pin') == bench then
          o.fields = { 'pin', '' }
        end
        local err = CARD.move(id, w, o)
        if err then return { 'REFUSED', id, err, tostring(requeued), tostring(repointed) } end
        if w == 'done' then
          repointed = repointed + 1
        else
          requeued = requeued + 1
          local S, label = string.match(id, '^s:([-a-z0-9]+):card:(.+)$')
          if S then redis.call('ZREM', 's:' .. S .. ':bench:' .. bench .. ':queue', label) end
        end
      end
    end

    local deleted = redis.call('DEL', 'ci:nomirror:' .. bench)
    for i = 4, #args do deleted = deleted + redis.call('DEL', pre .. args[i]) end
    for _, w in ipairs(RETIRE_PLACES) do deleted = deleted + redis.call('DEL', pre .. ':cards:' .. w) end
    redis.call('SREM', 'benches', bench)
    local at = retire_now_ms()
    redis.call('XADD', 'cap:log', 'MAXLEN', '~', '100000', '*',
      'kind', 'bench-retire', 'bench', bench, 'actor', actor, 'why', why,
      'requeued', tostring(requeued), 'repointed', tostring(repointed),
      'deleted', tostring(deleted), 'registered', tostring(registered), 'at', tostring(at))
    return { 'RETIRED', tostring(requeued), tostring(repointed), tostring(deleted), tostring(registered) }
  end

  redis.register_function('ns_bench_retire', bench_retire)
end
