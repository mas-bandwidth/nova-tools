-- Atomic table operations and read-only snapshots for internal/ntable.
-- Every public table operation is one exchange after connection setup.
--
-- Keys (internal/ntable/ntable.go):
--   table:<t>               HASH order, footer, created_at, col:<name>
--   table:<t>:rows          ZSET row key -> rank
--   table:<t>:row:<r>       HASH label, exclude, owner, key:<col> (a bound cell)
--   table:<t>:cell:<r>:<c>  ZSET an owned cell
--
-- Callers: the nova-table tool under whichever seat runs it, and the sprint
-- table loop (the coordinator seat). Snapshot/list/member functions are read-only.

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


-- Table operations resolve shape and bindings inside the same call as the
-- operation. A cold read is a snapshot; it never guesses yesterday's shape.
do
  local T = {}
  function T.hash(key)
    local flat = redis.call('HGETALL', key)
    local h = {}
    for i = 1, #flat, 2 do h[flat[i]] = flat[i + 1] end
    return h, flat
  end
  function T.def(name)
    local h, flat = T.hash('table:' .. name)
    if not h.order then return nil, {'REFUSED', 'NOTABLE'} end
    local cols, seen = {}, {}
    for col in string.gmatch(h.order, '[^,]+') do
      local proj, fold, width = string.match(h['col:' .. col] or '', '^([^:]+):([^:]+):([^:]+):')
      local valid = {count=true,members=true,first=true,last=true,text=true}
      local w = tonumber(width)
      if not valid[proj or ''] or seen[col] or not w or w < 0 or w ~= math.floor(w) or
        not (fold == 'none' or ((fold == 'sum' or fold == 'max') and proj == 'count') or
          (fold == 'union' and proj ~= 'count' and proj ~= 'text')) then
        return nil, {'REFUSED', 'DEFINITION', col}
      end
      seen[col] = true
      cols[#cols + 1] = {name = col, projection = proj}
    end
    if #cols == 0 then return nil, {'REFUSED', 'DEFINITION', ''} end
    return {cols = cols, h = h, flat = flat}
  end
  function T.rowkey(name, row) return 'table:' .. name .. ':row:' .. row end
  function T.cellkey(name, row, col) return 'table:' .. name .. ':cell:' .. row .. ':' .. col end
  function T.col(def, name)
    for _, col in ipairs(def.cols) do if col.name == name then return col end end
  end
  function T.cell(name, row, col, write)
    local def, err = T.def(name)
    if not def then return nil, err end
    if not redis.call('ZSCORE', 'table:' .. name .. ':rows', row) then return nil, {'REFUSED', 'NOROW', row} end
    local column = T.col(def, col)
    if not column then return nil, {'REFUSED', 'NOCOL', row, col} end
    if column.projection == 'text' then return nil, {'REFUSED', 'TEXT', row, col} end
    local h = T.hash(T.rowkey(name, row))
    local bound = h['key:' .. col]
    if write and bound and bound ~= '' then return nil, {'REFUSED', 'BOUND', row, col, bound, h.owner or ''} end
    return {key = bound and bound ~= '' and bound or T.cellkey(name, row, col), exclude = h.exclude or ''}
  end
  -- Check every write's permission before the first mutation. Redis does
  -- not roll a function back if a later command fails.
  function T.apply(commands)
    for _, cmd in ipairs(commands) do
      if not redis.acl_check_cmd(unpack(cmd)) then return {'REFUSED', 'NOPERM', cmd[1], cmd[2]} end
    end
    for _, cmd in ipairs(commands) do redis.call(unpack(cmd)) end
  end
  function T.hset(commands, key, h)
    local cmd = {'HSET', key}
    for k, v in pairs(h) do cmd[#cmd + 1] = k; cmd[#cmd + 1] = v end
    if #cmd > 2 then commands[#commands + 1] = cmd end
  end
  function T.rowfields(name, def, row, spec)
    local h = {}
    if spec.label and spec.label ~= '' then h.label = spec.label end
    if spec.exclude and spec.exclude ~= '' then h.exclude = spec.exclude end
    if spec.owner and spec.owner ~= '' then h.owner = spec.owner end
    for col, key in pairs(spec.binds or {}) do
      local c = T.col(def, col)
      if not c then return nil, {'REFUSED', 'NOCOL', row, col} end
      if c.projection == 'text' then return nil, {'REFUSED', 'TEXT', row, col} end
      if key == '' then return nil, {'REFUSED', 'BINDKEY', row, col} end
      h['key:' .. col] = key
    end
    return h
  end
  function T.remove(commands, name, def, row, h)
    for _, col in ipairs(def.cols) do
      -- A bound set has another writer, even when it happens to have the
      -- same key as this table's normally owned cell.
      if col.projection ~= 'text' and (not h['key:' .. col.name] or h['key:' .. col.name] == '') then
        commands[#commands + 1] = {'DEL', T.cellkey(name, row, col.name)}
      end
    end
    commands[#commands + 1] = {'DEL', T.rowkey(name, row)}
    commands[#commands + 1] = {'ZREM', 'table:' .. name .. ':rows', row}
  end
  function T.create(name, fields, commands)
    local have = T.hash('table:' .. name)
    if next(have) then
      for k, v in pairs(fields) do
        if k ~= 'created_at' and have[k] ~= v then return nil, {'REFUSED', 'EXISTS'} end
      end
      return T.def(name)
    end
    redis.call('SCARD', 'tables') -- type check before either write
    T.hset(commands, 'table:' .. name, fields)
    commands[#commands + 1] = {'SADD', 'tables', name}
    local cols = {}
    for col in string.gmatch(fields.order, '[^,]+') do cols[#cols + 1] = {name = col, projection = string.match(fields['col:' .. col], '^([^:]+):')} end
    return {h = fields, cols = cols}
  end
  redis.register_function('ns_table_create', function(keys, args)
    local commands = {}
    local _, err = T.create(args[1], cjson.decode(args[2]), commands)
    if err then return err end
    return T.apply(commands) or {'OK'}
  end)
  redis.register_function('ns_table_row_add', function(keys, args)
    local name, row, spec = args[1], args[2], cjson.decode(args[3])
    local def, err = T.def(name)
    if not def then return err end
    local h, why = T.rowfields(name, def, row, spec)
    if not h then return why end
    T.hash(T.rowkey(name, row)) -- refuse a corrupt row before rewriting it
    local rowsKey = 'table:' .. name .. ':rows'
    local rank = redis.call('ZSCORE', rowsKey, row)
    if not rank then
      local tail = redis.call('ZRANGE', rowsKey, -1, -1, 'WITHSCORES')
      rank = tostring((tonumber(tail[2]) or 0) + 1)
    end
    local commands = {{'DEL', T.rowkey(name, row)}}
    T.hset(commands, T.rowkey(name, row), h)
    commands[#commands + 1] = {'ZADD', rowsKey, rank, row}
    local refusal = T.apply(commands)
    if refusal then return refusal end
    local _, flat = T.hash(T.rowkey(name, row))
    return {'ROW', def.flat, flat}
  end)
  redis.register_function('ns_table_row_del', function(keys, args)
    local name, row = args[1], args[2]
    local def, err = T.def(name)
    if not def then return err end
    if not redis.call('ZSCORE', 'table:' .. name .. ':rows', row) then return {'OK', 0} end
    local h, commands = T.hash(T.rowkey(name, row)), {}
    T.remove(commands, name, def, row, h)
    return T.apply(commands) or {'OK', 1}
  end)
  function T.delete(op, args)
      local name = args[1]
      local def, err = T.def(name)
      if not def then return err end
      local rows = redis.call('ZRANGE', 'table:' .. name .. ':rows', 0, -1)
      local commands = {}
      if op == 'drop' then redis.call('SISMEMBER', 'tables', name) end
      for _, row in ipairs(rows) do
        local h = T.hash(T.rowkey(name, row))
        if op == 'clear' then
          for _, col in ipairs(def.cols) do
            local bound = h['key:' .. col.name]
            if bound and bound ~= '' then return {'REFUSED', 'BOUND', row, col.name, bound, h.owner or ''} end
          end
        end
        T.remove(commands, name, def, row, h)
      end
      commands[#commands + 1] = {'DEL', 'table:' .. name .. ':rows'}
      if op == 'drop' then
        commands[#commands + 1] = {'DEL', 'table:' .. name}
        commands[#commands + 1] = {'SREM', 'tables', name}
      end
      return T.apply(commands) or {'OK', #rows}
  end
  redis.register_function('ns_table_drop', function(keys,args) return T.delete('drop',args) end)
  redis.register_function('ns_table_clear', function(keys,args) return T.delete('clear',args) end)
  function T.writecell(op, args)
      local name, row, col, member = args[1], args[2], args[3], args[4]
      local src, err = T.cell(name, row, col, true)
      if not src then return err end
      local commands, target = {}, src.key
      redis.call('ZCARD', src.key) -- wrong type must refuse before mutation
      if op == 'add' then
        local score = tonumber(args[5])
        if not score or score ~= score then return {'REFUSED', 'SCORE'} end
        commands[1] = {'ZADD', src.key, args[5], member}
      elseif op == 'remove' then commands[1] = {'ZREM', src.key, member}
      else
        local dst, why = T.cell(name, row, args[5], true)
        if not dst then return why end
        redis.call('ZCARD', dst.key)
        local score = redis.call('ZSCORE', src.key, member)
        if not score then return {'REFUSED', 'NOTMEMBER', row, col} end
        target = dst.key
        commands = {{'ZREM', src.key, member}, {'ZADD', dst.key, score, member}}
      end
      return T.apply(commands) or {'OK', redis.call('ZCARD', target)}
  end
  redis.register_function('ns_table_cell_add', function(keys,args) return T.writecell('add',args) end)
  redis.register_function('ns_table_cell_remove', function(keys,args) return T.writecell('remove',args) end)
  redis.register_function('ns_table_cell_move', function(keys,args) return T.writecell('move',args) end)
  redis.register_function('ns_table_bind', function(keys, args)
    local name, spec = args[1], cjson.decode(args[2])
    local commands = {}
    local def, err = T.create(name, spec.fields, commands)
    if not def then return err end
    local old = redis.call('ZRANGE', 'table:' .. name .. ':rows', 0, -1)
    local keep = {}
    for i, row in ipairs(spec.rows) do
      if keep[row.key] then return {'REFUSED', 'TWICE', row.key} end
      keep[row.key] = true
      T.hash(T.rowkey(name, row.key))
      local h, why = T.rowfields(name, def, row.key, row)
      if not h then return why end
      commands[#commands + 1] = {'DEL', T.rowkey(name, row.key)}
      T.hset(commands, T.rowkey(name, row.key), h)
      commands[#commands + 1] = {'ZADD', 'table:' .. name .. ':rows', i, row.key}
    end
    for _, row in ipairs(old) do
      if not keep[row] then T.remove(commands, name, def, row, T.hash(T.rowkey(name, row))) end
    end
    return T.apply(commands) or {'OK'}
  end)
  function T.members(key, exclude)
    local values = redis.call('ZRANGE', key, 0, -1, 'WITHSCORES')
    local kept = {}
    for i = 1, #values, 2 do
      if exclude == '' or values[i] ~= exclude then kept[#kept + 1] = values[i]; kept[#kept + 1] = values[i + 1] end
    end
    return kept
  end
  redis.register_function{function_name = 'ns_table_members', flags = {'no-writes'}, callback = function(keys, args)
    local cell, err = T.cell(args[1], args[2], args[3], false)
    if not cell then return err end
    return {'MEMBERS', T.members(cell.key, cell.exclude)}
  end}
  redis.register_function{function_name = 'ns_table_read', flags = {'no-writes'}, callback = function(keys, args)
    local name, mode = args[1], args[2]
    local def, err = T.def(name)
    if not def then return err end
    local out = {'TABLE', def.flat, {}}
    for _, row in ipairs(redis.call('ZRANGE', 'table:' .. name .. ':rows', 0, -1)) do
      local h, flat = T.hash(T.rowkey(name, row))
      local cells = {}
      if mode ~= 'shape' then
        for _, col in ipairs(def.cols) do
          local value = {'OK', 0, {}}
          if col.projection ~= 'text' then
            local bound = h['key:' .. col.name]
            local key = bound and bound ~= '' and bound or T.cellkey(name, row, col.name)
            -- A missing/unreadable cell stays unknown, never a false zero.
            local count = redis.pcall('ZCARD', key)
            if type(count) == 'table' and count.err then value = {'UNREAD', count.err}
            elseif col.projection == 'count' then
              if h.exclude and h.exclude ~= '' and redis.call('ZSCORE', key, h.exclude) then count = count - 1 end
              value = {'OK', count, {}}
            else
              local members = T.members(key, h.exclude or '')
              value = {'OK', #members / 2, members}
            end
          end
          cells[#cells + 1] = value
        end
      end
      out[3][#out[3] + 1] = {row, flat, cells}
    end
    return out
  end}
  redis.register_function{function_name = 'ns_table_list', flags = {'no-writes'}, callback = function(keys, args)
    local names = redis.call('SMEMBERS', 'tables')
    table.sort(names)
    local out = {'TABLES'}
    for _, name in ipairs(names) do
      local def, err = T.def(name)
      if not def then return err end
      out[#out + 1] = {name, #def.cols, redis.call('ZCARD', 'table:' .. name .. ':rows')}
    end
    return out
  end}
end
