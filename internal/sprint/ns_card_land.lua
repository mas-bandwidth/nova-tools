-- Redis Function: ns_card_land
-- Layer 2 CardMachine Dual-Table Atomic Operation (Action 11: Land)
--
-- KEYS[1]: table:streams (primary lifecycle table)
-- KEYS[2]: table:fleet (consumer allocation table; or member hash if #keys == 2)
-- KEYS[3]: table::member:<card> (optional if #keys == 3)
--
-- ARGV[1]: WriteOptions JSON ({"epoch":"...", "actor":"...", "fence":"...", "idem":"..."})
-- ARGV[2]: card ID (e.g. "c1", "card-42")
--
-- Invariants enforced:
-- - TerminalIsQuiet: Merging/landed card must have zero live copies in fleet (Finding 1 fix).
-- - Transition Guard: Primary card must be in "merging" to land.
-- - Dual-Table Atomic Commit: Transitions primary to "landed" in streams,
--   retires any lingering fleet copies, updates place:streams and place:fleet atomically.

local function cm_now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function cell_key(table_name, epoch, row, col)
  local prefix = 'table:' .. table_name
  if epoch and epoch ~= '' and epoch ~= '0' then
    prefix = prefix .. ':' .. tostring(epoch)
  end
  return prefix .. ':cell:' .. row .. ':' .. col
end

local function refuse(reason, ...)
  local res = { 'REFUSED', reason }
  local extra = { ... }
  for i = 1, #extra do
    res[#res + 1] = tostring(extra[i])
  end
  return res
end

local function get_active_epoch(table_ref)
  local table_key = table_ref
  if not string.match(table_key, '^table:') then
    table_key = 'table:' .. table_ref
  end
  local epoch_key = redis.call('HGET', table_key, 'epoch_key')
  if not epoch_key or epoch_key == '' then
    epoch_key = redis.call('HGET', table_key .. ':identity', 'epoch_key')
  end
  local epoch_field = redis.call('HGET', table_key, 'epoch_field')
  if not epoch_field or epoch_field == '' then
    epoch_field = redis.call('HGET', table_key .. ':identity', 'epoch_field')
  end
  if not epoch_field or epoch_field == '' then
    epoch_field = 'n'
  end

  if epoch_key and epoch_key ~= '' then
    local val = redis.call('HGET', epoch_key, epoch_field)
    if val and val ~= '' then
      return tostring(val), true
    end
    return '0', true
  end

  local id_epoch = redis.call('HGET', table_key .. ':identity', 'epoch')
  if id_epoch and id_epoch ~= '' then
    return tostring(id_epoch), true
  end
  local root_epoch = redis.call('HGET', table_key, 'epoch')
  if root_epoch and root_epoch ~= '' then
    return tostring(root_epoch), true
  end

  local exists = redis.call('EXISTS', table_key)
  if exists == 0 then
    exists = redis.call('EXISTS', table_key .. ':identity')
  end
  if exists == 1 then
    return '0', true
  end

  return nil, false
end

local function ensure_type(key, expected)
  local t = redis.call('TYPE', key)
  local kind = (type(t) == 'table' and t.ok) or t
  if kind ~= 'none' and kind ~= expected then
    return false, 'WRONGTYPE Operation against a key holding the wrong kind of value: key ' .. key .. ' is ' .. tostring(kind) .. ', expected ' .. expected
  end
  return true, nil
end

redis.register_function('ns_sprint_card_land', function(keys, args)
  if #keys < 2 then
    return refuse('ARGS', 'insufficient keys: expected at least streams and member record')
  end
  if #args < 2 then
    return refuse('ARGS', 'insufficient args: expected opts, card')
  end

  local streams_table = keys[1]
  local fleet_table = 'table:fleet'
  local member_record_key = keys[2]

  if #keys >= 3 then
    fleet_table = keys[2]
    member_record_key = keys[3]
  end

  local ok, opts = pcall(cjson.decode, args[1] or '{}')
  if not ok or type(opts) ~= 'table' then
    return refuse('ARGS', 'malformed write options JSON')
  end

  local card = tostring(args[2] or '')
  if card == '' then
    return refuse('ARGS', 'card ID required')
  end

  local actor = tostring(opts.actor or '')
  local epoch = tostring(opts.epoch or '0')

  -- 1. Epoch Fencing Check
  local active_epoch, configured = get_active_epoch(streams_table)
  if configured then
    if epoch ~= active_epoch then
      return refuse('STALE', epoch, active_epoch)
    end
  else
    active_epoch = epoch
  end

  -- 2. Inspect Primary Card Record
  local card_exists = redis.call('EXISTS', member_record_key)
  if card_exists == 0 then
    return refuse('CARDNOTFOUND', card)
  end

  local where = redis.call('HGET', member_record_key, 'where') or ''
  local stream = redis.call('HGET', member_record_key, 'stream') or 'main'
  local live_copy = redis.call('HGET', member_record_key, 'copy') or ''
  local reads_json = redis.call('HGET', member_record_key, 'reads') or '[]'

  -- Land precondition: Card must be in "merging" state
  if where ~= 'merging' then
    if where == 'landed' then
      return refuse('TERMINALISQUIET', card, 'card is already landed')
    end
    return refuse('INVALIDSTATE', card, 'cannot land card from state: ' .. where)
  end

  local merging_cell = cell_key('streams', active_epoch, stream, 'merging')
  local landed_cell = cell_key('streams', active_epoch, stream, 'landed')
  local rev_streams_key = streams_table .. ':revision'
  local rev_fleet_key = fleet_table .. ':revision'
  local changes_key = streams_table .. ':changes'

  -- Preflight type checking before mutation
  local ok_t, err_t = ensure_type(changes_key, 'stream')
  if not ok_t then return redis.error_reply(err_t) end

  ok_t, err_t = ensure_type(rev_streams_key, 'hash')
  if not ok_t then return redis.error_reply(err_t) end

  ok_t, err_t = ensure_type(rev_fleet_key, 'hash')
  if not ok_t then return redis.error_reply(err_t) end

  ok_t, err_t = ensure_type(merging_cell, 'zset')
  if not ok_t then return redis.error_reply(err_t) end

  ok_t, err_t = ensure_type(landed_cell, 'zset')
  if not ok_t then return redis.error_reply(err_t) end

  ok_t, err_t = ensure_type(member_record_key, 'hash')
  if not ok_t then return redis.error_reply(err_t) end

  if live_copy ~= '' then
    local copy_key = 'table::member:' .. live_copy
    ok_t, err_t = ensure_type(copy_key, 'hash')
    if not ok_t then return redis.error_reply(err_t) end
  end

  local now_ms = cm_now()
  local now_str = tostring(now_ms)

  -- 3. Dual-Table Atomic Transition
  -- 3a. In streams table: move merging -> landed
  redis.call('ZREM', merging_cell, card)
  redis.call('ZADD', landed_cell, now_ms, card)

  -- 3b. In fleet table: retire any active copies (Finding 1 fix: TerminalIsQuiet)
  -- Retire primary work/fix copy if present
  if live_copy ~= '' then
    local copy_key = 'table::member:' .. live_copy
    local consumer = redis.call('HGET', copy_key, 'consumer') or ''
    local copy_where = redis.call('HGET', copy_key, 'where') or ''

    if consumer ~= '' and copy_where ~= '' then
      -- Remove from live cell in fleet
      local old_cell = cell_key('fleet', active_epoch, consumer, copy_where)
      redis.call('ZREM', old_cell, live_copy)
      -- Move to ok cell
      local ok_cell = cell_key('fleet', active_epoch, consumer, 'ok')
      redis.call('ZADD', ok_cell, now_ms, live_copy)

      redis.call('HSET', copy_key,
        'where', 'ok',
        'place:fleet', consumer .. ':ok',
        'updated_at', now_str
      )
    end
  end

  -- Retire any open readers in reads list
  local ok_reads, reads = pcall(cjson.decode, reads_json)
  if ok_reads and type(reads) == 'table' then
    for _, read_id in ipairs(reads) do
      local r_id = tostring(read_id)
      if r_id ~= '' then
        local read_key = 'table::member:' .. r_id
        local r_consumer = redis.call('HGET', read_key, 'consumer') or ''
        local r_where = redis.call('HGET', read_key, 'where') or ''
        if r_consumer ~= '' and r_where ~= '' then
          local old_r_cell = cell_key('fleet', active_epoch, r_consumer, r_where)
          redis.call('ZREM', old_r_cell, r_id)
          local fail_cell = cell_key('fleet', active_epoch, r_consumer, 'fail')
          redis.call('ZADD', fail_cell, now_ms, r_id)

          redis.call('HSET', read_key,
            'where', 'fail',
            'place:fleet', r_consumer .. ':fail',
            'updated_at', now_str
          )
        end
      end
    end
  end

  -- 3c. Update primary card record
  redis.call('HSET', member_record_key,
    'where', 'landed',
    'place:streams', stream .. ':landed',
    'ok', 'ok',
    'copy', '',
    'reads', '[]',
    'updated_at', now_str
  )

  -- 4. Sequence & Revision Updates on both tables
  local rev_streams = redis.call('HINCRBY', streams_table .. ':revision', 'n', 1)
  redis.call('HINCRBY', fleet_table .. ':revision', 'n', 1)
  local before = rev_streams - 1
  local after = rev_streams

  -- 5. Durable Event Logging
  local event_id = redis.call('XADD', streams_table .. ':changes', '*',
    'verb', 'land',
    'card', card,
    'stream', stream,
    'epoch', active_epoch,
    'actor', actor,
    'rev_before', tostring(before),
    'rev_after', tostring(after),
    'outcome', 'OK'
  )

  local receipt = { 'RECEIPT', event_id, tostring(active_epoch), tostring(before), tostring(after), 'OK' }
  return { 'OK', receipt }
end)
