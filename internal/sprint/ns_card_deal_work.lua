-- Redis Function: ns_card_deal_work
-- Layer 2 CardMachine Dual-Table Atomic Operation (Action 3: DealWork)
--
-- KEYS[1]: table:streams (primary lifecycle table)
-- KEYS[2]: table:fleet (consumer allocation table)
-- KEYS[3]: table::member:<card> (primary member record hash)
--
-- ARGV[1]: WriteOptions JSON ({"epoch":"...", "actor":"...", "fence":"...", "idem":"..."})
-- ARGV[2]: card ID (e.g. "c1", "card-42")
-- ARGV[3]: consumer ID / worker ID (e.g. "bench:darwin-arm64-1", "worker-1")
-- ARGV[4]: score (optional float priority / score)
--
-- Invariants enforced:
-- - WorkingHasCopy: Primary in "working" must have a live copy in fleet.
-- - OneLiveWork: Primary holds at most one live work/fix copy.
-- - Room(k) > 0: Consumer must have free slot capacity.
-- - DepsMet: Primary card dependencies must be satisfied.
-- - Dual-Table Atomic Commit: Primary moves to "working" in streams,
--   copy moves to "working" in fleet with worker ID assigned.

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

redis.register_function('ns_card_deal_work', function(keys, args)
  if #keys < 3 then
    return refuse('ARGS', 'insufficient keys: expected streams, fleet, and member hash')
  end
  if #args < 3 then
    return refuse('ARGS', 'insufficient args: expected opts, card, consumer')
  end

  local streams_table = keys[1]
  local fleet_table = keys[2]
  local member_record_key = keys[3]

  local ok, opts = pcall(cjson.decode, args[1] or '{}')
  if not ok or type(opts) ~= 'table' then
    return refuse('ARGS', 'malformed write options JSON')
  end

  local card = tostring(args[2] or '')
  local consumer = tostring(args[3] or '')
  local score_val = tonumber(args[4] or '0') or 0

  if card == '' then return refuse('ARGS', 'card ID required') end
  if consumer == '' then return refuse('ARGS', 'consumer ID required') end

  local actor = tostring(opts.actor or '')
  local epoch = tostring(opts.epoch or '0')

  -- 1. Epoch Fencing Check
  local active_epoch = redis.call('HGET', streams_table .. ':identity', 'epoch')
  if active_epoch and active_epoch ~= '' and epoch ~= '0' and epoch ~= active_epoch then
    return refuse('STALE', epoch, active_epoch)
  end
  if not active_epoch or active_epoch == '' then
    active_epoch = epoch
  end

  -- 2. Inspect Primary Card Record
  local card_exists = redis.call('EXISTS', member_record_key)
  if card_exists == 0 then
    return refuse('CARDNOTFOUND', card)
  end

  local where = redis.call('HGET', member_record_key, 'where') or ''
  local stream = redis.call('HGET', member_record_key, 'stream') or 'main'
  local ncut = tonumber(redis.call('HGET', member_record_key, 'ncut') or '0') or 0
  local current_copy = redis.call('HGET', member_record_key, 'copy') or ''
  local deps_unmet = redis.call('HGET', member_record_key, 'deps_unmet')

  if deps_unmet == '1' or deps_unmet == 'true' then
    return refuse('DEPSNOTMET', card)
  end

  -- Primary state validation: can only deal from waiting or ready
  if where == 'working' then
    return refuse('WORKINGHASCOPY', card)
  end
  if where == 'merging' or where == 'landed' or where == 'done' then
    return refuse('TERMINALISQUIET', card)
  end
  if where ~= 'waiting' and where ~= 'ready' then
    return refuse('INVALIDSTATE', card, where)
  end

  -- Bare(c) invariant: primary must not currently hold an active live copy
  if current_copy ~= '' then
    local live_state = redis.call('HGET', 'table::member:' .. current_copy, 'where')
    if live_state == 'ready' or live_state == 'working' then
      return refuse('ONELIVEWORK', card, current_copy)
    end
  end

  -- 3. Consumer Capacity Verification (Room(k) > 0)
  -- Default slots = 4 if not configured
  local slots = tonumber(redis.call('HGET', fleet_table .. ':consumer:' .. consumer, 'slots') or '4') or 4
  local working_cell = cell_key('fleet', active_epoch, consumer, 'working')
  local ready_cell = cell_key('fleet', active_epoch, consumer, 'ready')

  local working_cnt = redis.call('ZCARD', working_cell)
  local ready_cnt = redis.call('ZCARD', ready_cell)
  local room = slots - working_cnt - ready_cnt

  if room <= 0 then
    return refuse('NOROOM', consumer, tostring(room))
  end

  -- 4. Generate New Copy ID
  local attempt = ncut + 1
  local copy_id = card .. ':' .. tostring(attempt)
  local copy_record_key = 'table::member:' .. copy_id

  local now_ms = cm_now()
  local now_str = tostring(now_ms)
  local score = score_val ~= 0 and score_val or now_ms

  -- 5. Dual-Table Atomic State Transition
  -- 5a. streams table: move primary card where -> working
  local old_stream_cell = cell_key('streams', active_epoch, stream, where)
  local new_stream_cell = cell_key('streams', active_epoch, stream, 'working')

  redis.call('ZREM', old_stream_cell, card)
  redis.call('ZADD', new_stream_cell, score, card)

  -- Update primary card record
  redis.call('HSET', member_record_key,
    'where', 'working',
    'place:streams', stream .. ':working',
    'copy', copy_id,
    'ncut', tostring(attempt),
    'worker', consumer,
    'author', consumer,
    'updated_at', now_str
  )

  -- 5b. fleet table: assign worker ID and place copy in working column
  -- (Moves/sets copy to working in place:fleet)
  redis.call('ZADD', working_cell, score, copy_id)

  -- Create copy member record
  redis.call('HSET', copy_record_key,
    'id', copy_id,
    'epoch', active_epoch,
    'primary', card,
    'consumer', consumer,
    'worker', consumer,
    'leg', 'work',
    'where', 'working',
    'place:fleet', consumer .. ':working',
    'attempt', tostring(attempt),
    'score', tostring(score),
    'leased', '1',
    'created_at', now_str,
    'updated_at', now_str
  )

  -- 6. Sequence & Revision Updates on both tables
  local rev_streams = redis.call('HINCRBY', streams_table .. ':revision', 'n', 1)
  redis.call('HINCRBY', fleet_table .. ':revision', 'n', 1)
  local before = rev_streams - 1
  local after = rev_streams

  -- 7. Durable Event Logging
  local event_id = redis.call('XADD', streams_table .. ':changes', '*',
    'verb', 'deal_work',
    'card', card,
    'copy', copy_id,
    'consumer', consumer,
    'worker', consumer,
    'stream', stream,
    'epoch', active_epoch,
    'rev_before', tostring(before),
    'rev_after', tostring(after),
    'outcome', 'OK'
  )

  local receipt = { 'RECEIPT', event_id, tostring(active_epoch), tostring(before), tostring(after), 'OK' }
  return { 'OK', copy_id, receipt }
end)
