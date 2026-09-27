-- The two atomic writes of internal/ntable, the general Redis-backed table
-- whose every body cell is an ordered set (nova-table; the sprint table's
-- stream block is its first table). Everything else ntable writes is a plain
-- pipelined command; these two touch several keys and must land whole.
--
-- Keys (internal/ntable/ntable.go):
--   table:<t>               HASH order, footer, created_at, col:<name>
--   table:<t>:rows          ZSET row key -> rank
--   table:<t>:row:<r>       HASH label, exclude, owner, key:<col> (a bound cell)
--   table:<t>:cell:<r>:<c>  ZSET an owned cell
--
-- Callers: the nova-table tool under whichever seat runs it, and the sprint
-- table loop (the coordinator seat); no function here is FCALL_RO.

-- ns_oset_move: the ordered-set primitive's one atomic move. KEYS[1] the set
-- the member leaves, KEYS[2] the set it joins, ARGV[1] the member, ARGV[2]
-- its new score ('' keeps the score it had). Refused NOTMEMBER when KEYS[1]
-- does not hold it; nothing is written then.
redis.register_function('ns_oset_move', function(keys, args)
  local from, to, member = keys[1], keys[2], args[1]
  local score = redis.call('ZSCORE', from, member)
  if not score then
    return { 'REFUSED', 'NOTMEMBER' }
  end
  if args[2] and args[2] ~= '' then
    score = args[2]
  end
  redis.call('ZREM', from, member)
  redis.call('ZADD', to, score, member)
  return { 'MOVED', tostring(score) }
end)

-- ns_table_clear: every owned cell of the table emptied and every row
-- removed, the definition left. KEYS[1] table:<t>, ARGV[1] the table name.
-- Refused NOTABLE for a table the store does not define, and BOUND (with
-- the row, column, key and owner) when any cell is bound to a set owned
-- elsewhere: a view is never cleared here, and nothing is written then.
redis.register_function('ns_table_clear', function(keys, args)
  local name = args[1]
  local def = keys[1]
  local order = redis.call('HGET', def, 'order')
  if not order then
    return { 'REFUSED', 'NOTABLE' }
  end
  local cols = {}
  for col in string.gmatch(order, '[^,]+') do
    cols[#cols + 1] = col
  end
  local rowsKey = 'table:' .. name .. ':rows'
  local rows = redis.call('ZRANGE', rowsKey, 0, -1)
  local hashes = {}
  for i, row in ipairs(rows) do
    local rowKey = 'table:' .. name .. ':row:' .. row
    local h = redis.call('HGETALL', rowKey)
    local fields = {}
    for j = 1, #h, 2 do
      fields[h[j]] = h[j + 1]
    end
    for _, col in ipairs(cols) do
      local bound = fields['key:' .. col]
      if bound and bound ~= '' then
        return { 'REFUSED', 'BOUND', row, col, bound, fields['owner'] or '' }
      end
    end
    hashes[i] = rowKey
  end
  for i, row in ipairs(rows) do
    for _, col in ipairs(cols) do
      redis.call('DEL', 'table:' .. name .. ':cell:' .. row .. ':' .. col)
    end
    redis.call('DEL', hashes[i])
  end
  redis.call('DEL', rowsKey)
  return { 'CLEARED', #rows }
end)
