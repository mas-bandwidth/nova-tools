-- Redis Function: ns_card_push
-- Layer 2 CardMachine Dual-Table Atomic Operation (Action 1: Push)
--
-- KEYS[1]: table:streams (logical table root)
-- KEYS[2]: table::member:<card> (member hash record)
--
-- ARGV[1]: WriteOptions JSON ({"epoch":"...", "actor":"...", "fence":"...", "idem":"..."})
-- ARGV[2]: card ID (e.g. "c1", "card-42")
-- ARGV[3]: stream name (e.g. "main", "core")
--
-- Invariants enforced:
-- - ONE PLACE (streams): Card must not already exist or have active placement in streams
-- - Epoch fencing: opts.epoch matches activeEpoch if configured
-- - Actor/Consumer verification: actor must be present and valid
-- - Link consistency: place:streams = "<stream>:waiting"

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

redis.register_function('ns_sprint_card_push', function(keys, args)
  if #keys < 2 then
    return refuse('ARGS', 'insufficient keys: expected table:streams and member hash')
  end
  if #args < 3 then
    return refuse('ARGS', 'insufficient args: expected opts, card, stream')
  end

  local streams_table = keys[1]
  local member_record_key = keys[2]

  local ok, opts = pcall(cjson.decode, args[1] or '{}')
  if not ok or type(opts) ~= 'table' then
    return refuse('ARGS', 'malformed write options JSON')
  end

  local card = tostring(args[2] or '')
  local stream = tostring(args[3] or '')

  if card == '' then
    return refuse('ARGS', 'card ID required')
  end
  if stream == '' then
    return refuse('ARGS', 'stream name required')
  end

  local actor = tostring(opts.actor or '')
  local epoch = tostring(opts.epoch or '0')

  -- 1. Consumer/Actor Verification
  if actor == '' then
    return refuse('CONSUMER', 'actor/consumer identity required for push')
  end

  -- 2. Epoch Verification
  local active_epoch, configured = get_active_epoch(streams_table)
  if configured then
    if epoch ~= active_epoch then
      return refuse('STALE', epoch, active_epoch)
    end
  else
    active_epoch = epoch
  end

  -- 3. Duplicate Member Precondition Check (ONE PLACE)
  local existing_where = redis.call('HGET', member_record_key, 'where')
  local existing_place = redis.call('HGET', member_record_key, 'place:streams')
  if (existing_where and existing_where ~= '' and existing_where ~= 'null') or
     (existing_place and existing_place ~= '') then
    return refuse('DUPLICATEMEMBER', card)
  end

  local waiting_cell = cell_key('streams', active_epoch, stream, 'waiting')
  local rev_key = streams_table .. ':revision'
  local changes_key = streams_table .. ':changes'

  -- Preflight type checking before mutation
  local ok_t, err_t = ensure_type(changes_key, 'stream')
  if not ok_t then return redis.error_reply(err_t) end

  ok_t, err_t = ensure_type(rev_key, 'hash')
  if not ok_t then return redis.error_reply(err_t) end

  ok_t, err_t = ensure_type(waiting_cell, 'zset')
  if not ok_t then return redis.error_reply(err_t) end

  ok_t, err_t = ensure_type(member_record_key, 'hash')
  if not ok_t then return redis.error_reply(err_t) end

  local now_ms = cm_now()
  local now_str = tostring(now_ms)

  -- 4. Single-Table Placement in streams: Row = stream, Col = "waiting"
  redis.call('ZADD', waiting_cell, now_ms, card)

  -- 5. Write Member Record Hash (Double Link: place:streams = stream .. ":waiting")
  local placement = stream .. ':waiting'
  redis.call('HSET', member_record_key,
    'id', card,
    'epoch', active_epoch,
    'stream', stream,
    'where', 'waiting',
    'place:streams', placement,
    'place:fleet', '',
    'copy', '',
    'reads', '[]',
    'pending', '0',
    'low', '0',
    'ncut', '0',
    'author', actor,
    'head', '0',
    'pr_head', '0',
    'ci', 'pending',
    'ok', '-',
    'created_at', now_str,
    'updated_at', now_str
  )

  -- 6. Sequence & Revision Advance
  local after = redis.call('HINCRBY', rev_key, 'n', 1)
  local before = after - 1

  -- 7. Durable Event Logging on table:streams:changes
  local event_id = redis.call('XADD', changes_key, '*',
    'verb', 'push',
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
