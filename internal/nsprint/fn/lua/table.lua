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


-- Stage and validate the whole operation before the first write. Redis does
-- not roll back a function on a later command error. This excludes resource
-- exhaustion/server failure; it does cover input, type, ACL and range errors.
do
  local T = {}
  function T.refuse(reason, ...) return {'REFUSED', reason, ...} end
  function T.hash(key)
    local flat, h = redis.call('HGETALL', key), {}
    for i = 1, #flat, 2 do h[flat[i]] = flat[i + 1] end
    return h, flat
  end
  function T.flat(h)
    local flat = {}
    for k, v in pairs(h) do flat[#flat + 1] = k; flat[#flat + 1] = v end
    return flat
  end
  -- Decimal strings preserve uint64 values beyond Lua's exact integer range.
  function T.uint(n)
    return type(n) == 'string' and (n == '0' or string.match(n, '^[1-9][0-9]*$')) and
      (#n < 20 or (#n == 20 and n <= '18446744073709551615'))
  end
  function T.next(n)
    if not T.uint(n) or n == '18446744073709551615' then return nil end
    local out, carry = '', 1
    for i = #n, 1, -1 do
      local v = tonumber(string.sub(n, i, i)) + carry
      if v == 10 then v = 0 else carry = 0 end
      out = tostring(v) .. out
    end
    if carry == 1 then out = '1' .. out end
    return out
  end
  function T.name(n) return type(n) == 'string' and string.match(n, '^[%w_][%w_.-]*$') end
  function T.word(n) return type(n) == 'string' and n ~= '' and not string.find(n, '%c') end
  function T.decode(s)
    local ok, v = pcall(cjson.decode, s or '')
    if ok and type(v) == 'table' then return v end
  end
  function T.config(h)
    return {epoch_key=h.epoch_key or '', epoch_field=h.epoch_field or 'n',
      member_prefix=h.member_prefix or 'table::member:'}
  end
  function T.sameconfig(a, b)
    a, b = T.config(a), T.config(b)
    return a.epoch_key == b.epoch_key and a.epoch_field == b.epoch_field and a.member_prefix == b.member_prefix
  end
  function T.shape(h)
    if type(h) ~= 'table' or type(h.order) ~= 'string' then return nil, T.refuse('DEFINITION') end
    local cols, seen, order = {}, {}, {}
    for k, v in pairs(h) do
      if type(k) ~= 'string' or type(v) ~= 'string' or
          not (k == 'order' or k == 'footer' or k == 'created_at' or k == 'epoch_key' or
          k == 'epoch_field' or k == 'member_prefix' or string.sub(k, 1, 4) == 'col:') then
        return nil, T.refuse('DEFINITION', tostring(k))
      end
    end
    for col in string.gmatch(h.order, '[^,]+') do
      local proj, fold, width = string.match(h['col:' .. col] or '', '^([^:]+):([^:]+):([^:]+):')
      local valid = {count=true,members=true,first=true,last=true,text=true}
      local w = tonumber(width)
      if not T.name(col) or not valid[proj or ''] or seen[col] or not T.uint(width) or #width > 19 or
        (#width == 19 and width > '9223372036854775807') or not w or w < 0 or w ~= math.floor(w) or
        not (fold == 'none' or ((fold == 'sum' or fold == 'max') and proj == 'count') or
          (fold == 'union' and proj ~= 'count' and proj ~= 'text')) then
        return nil, T.refuse('DEFINITION', col)
      end
      seen[col] = true
      order[#order + 1] = col
      cols[#cols + 1] = {name=col, projection=proj}
    end
    if #cols == 0 or table.concat(order, ',') ~= h.order then return nil, T.refuse('DEFINITION') end
    local cfg = T.config(h)
    if not T.word(cfg.member_prefix) or string.sub(cfg.member_prefix, -1) ~= ':' or
        (string.sub(cfg.member_prefix, 1, 6) == 'table:' and cfg.member_prefix ~= 'table::member:') or
        not T.word(cfg.epoch_field) or
        (cfg.epoch_key ~= '' and (not T.word(cfg.epoch_key) or cfg.epoch_key == 'tables' or
        string.sub(cfg.epoch_key, 1, 6) == 'table:' or
        string.sub(cfg.epoch_key, 1, #cfg.member_prefix) == cfg.member_prefix)) then
      return nil, T.refuse('CONFIG')
    end
    return cols
  end
  function T.prefix(name, epoch)
    return 'table:' .. name .. (epoch == '0' and '' or ':' .. epoch)
  end
  function T.open(name, fields, historical)
    if not T.name(name) then return nil, T.refuse('NAME') end
    local key = 'table:' .. name
    local template = T.hash(key)
    local identity = T.hash(key .. ':identity')
    if fields then
      local _, err = T.shape(fields)
      if err then return nil, err end
      if next(identity) and not T.sameconfig(identity, fields) then return nil, T.refuse('CONFIG') end
      if next(template) then
        for k, v in pairs(fields) do
          if k ~= 'created_at' and k ~= 'epoch_key' and k ~= 'epoch_field' and k ~= 'member_prefix' and template[k] ~= v then
            return nil, T.refuse('EXISTS')
          end
        end
        if not T.sameconfig(template, fields) then return nil, T.refuse('EXISTS') end
      end
    end
    local h = next(template) and template or fields
    local cfg = T.config(h or identity)
    local active = cfg.epoch_key == '' and '0' or (redis.call('HGET', cfg.epoch_key, cfg.epoch_field) or '0')
    if not T.uint(active) then return nil, T.refuse('EPOCH', active) end
    local epoch = historical or active
    if not T.uint(epoch) then return nil, T.refuse('EPOCH', tostring(epoch)) end
    local prefix = T.prefix(name, epoch)
    local snap = T.hash(prefix .. ':definition')
    if historical and snap.order then
      h = {}
      for k, v in pairs(snap) do if string.sub(k, 1, 1) ~= '_' then h[k] = v end end
      cfg = T.config(h)
    elseif historical and epoch ~= active then
      return nil, T.refuse('NOTABLE')
    end
    if not h then return nil, T.refuse('NOTABLE') end
    local cols, err = T.shape(h)
    if not cols then return nil, err end
    local revision = historical and snap._revision or redis.call('HGET', key .. ':revision', 'n')
    revision = revision or '0'
    if not T.uint(revision) then return nil, T.refuse('REVISION', revision) end
    return {name=name, key=key, prefix=prefix, epoch=epoch, active=active, revision=revision,
      h=h, cols=cols, cfg=cfg, snap=snap, present=snap._present ~= '0',
      newtemplate=not next(template), newidentity=not next(identity), commands={}, cells={}, members={}}
  end
  function T.def(name, historical)
    local d, err = T.open(name, nil, historical)
    if not d then return nil, err end
    if not d.present then return nil, T.refuse('NOTABLE') end
    return d
  end
  function T.flatdef(d)
    local flat = T.flat(d.h)
    flat[#flat + 1] = 'read_epoch'; flat[#flat + 1] = d.epoch
    flat[#flat + 1] = 'read_revision'; flat[#flat + 1] = d.revision
    return flat
  end
  function T.rowkey(d, row) return d.prefix .. ':row:' .. row end
  function T.cellkey(d, row, col) return d.prefix .. ':cell:' .. row .. ':' .. col end
  function T.rowskey(d) return d.prefix .. ':rows' end
  function T.col(d, name)
    for _, col in ipairs(d.cols) do if col.name == name then return col end end
  end
  function T.cell(d, row, col, write)
    if not redis.call('ZSCORE', T.rowskey(d), row) then return nil, T.refuse('NOROW', row) end
    local c = T.col(d, col)
    if not c then return nil, T.refuse('NOCOL', row, col) end
    if c.projection == 'text' then return nil, T.refuse('TEXT', row, col) end
    local h = T.hash(T.rowkey(d, row))
    local bound = h['key:' .. col]
    if write and bound and bound ~= '' then return nil, T.refuse('BOUND', row, col, bound, h.owner or '') end
    return {key=bound and bound ~= '' and bound or T.cellkey(d, row, col), exclude=h.exclude or ''}
  end
  function T.hset(commands, key, h)
    local cmd = {'HSET', key}
    for k, v in pairs(h) do cmd[#cmd + 1] = k; cmd[#cmd + 1] = v end
    if #cmd > 2 then commands[#commands + 1] = cmd end
  end
  function T.stage(d, ...) d.commands[#d.commands + 1] = {...} end
  function T.place(row, col) return row .. ':' .. col end
  function T.memberkey(d, id) return d.cfg.member_prefix .. id end
  function T.member(d, id)
    if not T.word(id) then return nil, nil, T.refuse('MEMBER') end
    local h = T.hash(T.memberkey(d, id))
    local exists = next(h) ~= nil
    local epoch = h.epoch or '0'
    if exists and epoch ~= d.epoch then return nil, nil, T.refuse('MEMBEREPOCH', id, epoch, d.epoch) end
    return h, exists
  end
  function T.record(d, id, place, record, exists)
    -- Legacy epoch-zero records acquire an explicit immutable epoch on first
    -- placement. Other record fields belong to their existing owner.
    if not record.epoch then T.stage(d, 'HSET', T.memberkey(d, id), 'epoch', d.epoch) end
    if place then T.stage(d, 'HSET', T.memberkey(d, id), 'place:' .. d.name, place)
    else T.stage(d, 'HDEL', T.memberkey(d, id), 'place:' .. d.name) end
  end
  function T.change(d, id, from, to, score)
    d.members[#d.members + 1] = {id=id, from=from or '', to=to or '', score=score or ''}
    if from then d.cells[from] = true end
    if to then d.cells[to] = true end
  end
  function T.unindexed(d, id)
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      local h = T.hash(T.rowkey(d, row))
      for _, col in ipairs(d.cols) do
        if redis.call('ZSCORE', T.cellkey(d, row, col.name), id) then
          return T.refuse('DRIFT', row, col.name, id)
        end
      end
    end
  end
  function T.rowfields(d, row, spec)
    if not T.word(row) or type(spec) ~= 'table' then return nil, T.refuse('ROW') end
    local h = {}
    for _, k in ipairs({'label', 'exclude', 'owner'}) do
      if spec[k] and type(spec[k]) ~= 'string' then return nil, T.refuse('ROW', row) end
      if spec[k] and spec[k] ~= '' then h[k] = spec[k] end
    end
    if spec.binds and type(spec.binds) ~= 'table' then return nil, T.refuse('BINDKEY', row) end
    for col, key in pairs(spec.binds or {}) do
      local c = T.col(d, col)
      if not c then return nil, T.refuse('NOCOL', row, col) end
      if c.projection == 'text' then return nil, T.refuse('TEXT', row, col) end
      if not T.word(key) then return nil, T.refuse('BINDKEY', row, col) end
      -- Reserve the entire table namespace, including future epochs/metadata.
      if key == 'tables' or string.sub(key, 1, 6) == 'table:' then return nil, T.refuse('OWNEDALIAS', row, col, key) end
      h['key:' .. col] = key
    end
    return h
  end
  function T.keep_owned(d, row, old, new)
    for _, col in ipairs(d.cols) do
      local prior, target = old['key:' .. col.name], new and new['key:' .. col.name]
      if (prior and prior ~= '') or col.projection == 'text' then
        local hidden = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
        if #hidden > 0 then return T.refuse('OCCUPIED', row, col.name, hidden) end
      end
      if col.projection ~= 'text' and (not prior or prior == '') and (not new or (target and target ~= '')) then
        local members = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
        if #members > 0 then return T.refuse('OCCUPIED', row, col.name, members) end
      end
    end
  end
  function T.remove(d, row, h)
    for _, col in ipairs(d.cols) do
      d.cells[T.place(row, col.name)] = true
      local bound = h['key:' .. col.name]
      if (bound and bound ~= '') or col.projection == 'text' then
        local hidden = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
        if #hidden > 0 then return T.refuse('OCCUPIED', row, col.name, hidden) end
      end
      if col.projection ~= 'text' and (not h['key:' .. col.name] or h['key:' .. col.name] == '') then
        local members = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1, 'WITHSCORES')
        for i = 1, #members, 2 do
          local id, score = members[i], members[i + 1]
          local record, exists, err = T.member(d, id)
          if err then return err end
          if not exists or record['place:' .. d.name] ~= T.place(row, col.name) then return T.refuse('DRIFT', row, col.name, id) end
          T.record(d, id, nil, record, exists)
          T.change(d, id, T.place(row, col.name), nil, score)
        end
        T.stage(d, 'DEL', T.cellkey(d, row, col.name))
        d.cells[T.place(row, col.name)] = true
      end
    end
    T.stage(d, 'DEL', T.rowkey(d, row))
    T.stage(d, 'ZREM', T.rowskey(d), row)
  end
  -- Untrimmed change streams are the receipt. All validation (including
  -- stream/revision bounds and ACLs) precedes all writes; XADD is last.
  function T.finish(d, verb, args, opts, reply)
    local after = T.next(d.revision)
    if not after then return T.refuse('REVISION', d.revision) end
    local stream = d.key .. ':changes'
    local kind = redis.call('TYPE', stream).ok
    if kind ~= 'none' and kind ~= 'stream' then return T.refuse('STREAMTYPE', stream) end
    if kind == 'stream' then
      local info = redis.call('XINFO', 'STREAM', stream)
      for i = 1, #info, 2 do
        if info[i] == 'last-generated-id' and info[i + 1] == '18446744073709551615-18446744073709551615' then
          return T.refuse('STREAMFULL', stream)
        end
      end
    end
    local outcome = #d.commands == 0 and not d.newtemplate and d.snap._present ~= '0' and 'noop' or 'changed'
    if d.newtemplate then
      redis.call('SCARD', 'tables')
      T.hset(d.commands, d.key, d.h)
      T.stage(d, 'SADD', 'tables', d.name)
    end
    if d.newidentity then T.hset(d.commands, d.key .. ':identity', d.cfg) end
    -- Materialised epochs retain their own definition after template removal.
    if d.newtemplate then T.stage(d, 'DEL', d.prefix .. ':definition') end
    if d.newtemplate or not d.snap.order then T.hset(d.commands, d.prefix .. ':definition', d.h) end
    T.stage(d, 'HSET', d.prefix .. ':definition', '_present', d.present and '1' or '0', '_revision', after)
    T.stage(d, 'HSET', d.key .. ':revision', 'n', after)
    local cells = {}
    for cell in pairs(d.cells) do cells[#cells + 1] = cell end
    table.sort(cells)
    local wireargs = {}
    for i = 1, #args - 1 do wireargs[#wireargs + 1] = args[i] end
    local event = {'XADD', stream, '*', 'verb', verb, 'args', cjson.encode(wireargs),
      'epoch', d.epoch, 'rev_before', d.revision, 'rev_after', after, 'actor', opts.actor or '',
      'fence', opts.fence or '', 'idem', opts.idem or '', 'cells', #cells == 0 and '[]' or cjson.encode(cells),
      'members', #d.members == 0 and '[]' or cjson.encode(d.members), 'outcome', outcome}
    T.stage(d, unpack(event))
    for _, cmd in ipairs(d.commands) do
      if not redis.acl_check_cmd(unpack(cmd)) then return T.refuse('NOPERM', cmd[1], cmd[2]) end
    end
    local id
    for _, cmd in ipairs(d.commands) do id = redis.call(unpack(cmd)) end
    local before = d.revision
    d.revision = after
    if type(reply) == 'function' then reply = reply() end
    reply[#reply + 1] = {'RECEIPT', id, d.epoch, before, after, outcome}
    return reply
  end
  function T.write(verb, argc, handler, declaration)
    return function(keys, args)
      if #args ~= argc then return T.refuse('ARGS', verb) end
      local opts = T.decode(args[#args])
      if not opts or not T.uint(opts.epoch) then return T.refuse('EPOCH', 'observed epoch required') end
      for _, k in ipairs({'actor','fence','idem'}) do
        if opts[k] and type(opts[k]) ~= 'string' then return T.refuse('OPTIONS', k) end
      end
      local spec, fields
      if declaration then
        spec = T.decode(args[2])
        if not spec then return T.refuse('DEFINITION') end
        fields = declaration == 'bind' and spec.fields or spec
        if type(fields) ~= 'table' then return T.refuse('DEFINITION') end
      end
      local d, err = T.open(args[1], fields)
      if not d then return err end
      if opts.epoch ~= d.active then return T.refuse('STALE', opts.epoch, d.active) end
      if not d.present and not declaration and verb ~= 'drop_definition' then return T.refuse('NOTABLE') end
      if declaration then d.present = true end
      local reply, why = handler(d, args, spec)
      if not reply then return why end
      return T.finish(d, verb, args, opts, reply)
    end
  end
  redis.register_function('ns_table_create', T.write('create', 3, function(d) return {'OK'} end, 'create'))
  redis.register_function('ns_table_row_add', T.write('row_add', 4, function(d, args)
    local row, spec = args[2], T.decode(args[3])
    local h, err = T.rowfields(d, row, spec)
    if not h then return nil, err end
    local old = T.hash(T.rowkey(d, row))
    local loss = T.keep_owned(d, row, old, h)
    if loss then return nil, loss end
    local rank = redis.call('ZSCORE', T.rowskey(d), row)
    if not rank then
      local tail = redis.call('ZRANGE', T.rowskey(d), -1, -1, 'WITHSCORES')
      local n = tonumber(tail[2]) or 0
      if n ~= n or n + 1 == n or n == math.huge then return nil, T.refuse('RANK') end
      rank = tostring(n + 1)
    end
    T.stage(d, 'DEL', T.rowkey(d, row))
    T.hset(d.commands, T.rowkey(d, row), h)
    T.stage(d, 'ZADD', T.rowskey(d), rank, row)
    for _, col in ipairs(d.cols) do d.cells[T.place(row, col.name)] = true end
    return function() return {'ROW', T.flatdef(d), T.flat(h)} end
  end))
  redis.register_function('ns_table_row_del', T.write('row_del', 3, function(d, args)
    local row = args[2]
    if not redis.call('ZSCORE', T.rowskey(d), row) then return {'OK', 0} end
    local loss = T.remove(d, row, T.hash(T.rowkey(d, row)))
    if loss then return nil, loss end
    return {'OK', 1}
  end))
  function T.delete(d, args, op)
    local rows = redis.call('ZRANGE', T.rowskey(d), 0, -1)
    for _, row in ipairs(rows) do
      local h = T.hash(T.rowkey(d, row))
      if op == 'clear' then
        for _, col in ipairs(d.cols) do
          local bound = h['key:' .. col.name]
          if bound and bound ~= '' then return nil, T.refuse('BOUND', row, col.name, bound, h.owner or '') end
        end
      end
      local err = T.remove(d, row, h)
      if err then return nil, err end
    end
    T.stage(d, 'DEL', T.rowskey(d))
    if op == 'drop' or op == 'drop_definition' then d.present = false end
    if op == 'drop_definition' then
      redis.call('SCARD', 'tables')
      T.stage(d, 'DEL', d.key)
      T.stage(d, 'SREM', 'tables', d.name)
    end
    return {'OK', #rows}
  end
  redis.register_function('ns_table_drop', T.write('drop', 2, function(d, args) return T.delete(d, args, 'drop') end))
  redis.register_function('ns_table_drop_definition', T.write('drop_definition', 2, function(d, args) return T.delete(d, args, 'drop_definition') end))
  redis.register_function('ns_table_clear', T.write('clear', 2, function(d, args) return T.delete(d, args, 'clear') end))
  function T.writecell(d, args, op)
    local row, col, id = args[2], args[3], args[4]
    local src, err = T.cell(d, row, col, true)
    if not src then return nil, err end
    local count = redis.call('ZCARD', src.key)
    local record, exists, why = T.member(d, id)
    if why then return nil, why end
    local placed, here = record['place:' .. d.name], T.place(row, col)
    if op == 'add' then
      local score = tonumber(args[5])
      if not score or score ~= score or score == math.huge or score == -math.huge then return nil, T.refuse('SCORE') end
      if placed then return nil, T.refuse('PLACED', placed, id) end
      local drift = T.unindexed(d, id)
      if drift then return nil, drift end
      T.stage(d, 'ZADD', src.key, args[5], id)
      T.record(d, id, here, record, exists)
      T.change(d, id, nil, here, args[5])
      count = count + 1
    elseif op == 'remove' then
      local score = redis.call('ZSCORE', src.key, id)
      if placed ~= here then
        if score then return nil, T.refuse('DRIFT', row, col, id) end
        return {'OK', count}
      end
      if not score then return nil, T.refuse('DRIFT', row, col, id) end
      T.stage(d, 'ZREM', src.key, id)
      T.record(d, id, nil, record, exists)
      T.change(d, id, here, nil, score)
      count = count - 1
    else
      local dst, problem = T.cell(d, row, args[5], true)
      if not dst then return nil, problem end
      count = redis.call('ZCARD', dst.key)
      local score = redis.call('ZSCORE', src.key, id)
      if not score then return nil, T.refuse('NOTMEMBER', row, col) end
      if placed ~= here then return nil, T.refuse('DRIFT', row, col, id) end
      if dst.key ~= src.key and redis.call('ZSCORE', dst.key, id) then return nil, T.refuse('DRIFT', row, args[5], id) end
      if dst.key ~= src.key then
        T.stage(d, 'ZREM', src.key, id)
        T.stage(d, 'ZADD', dst.key, score, id)
        T.record(d, id, T.place(row, args[5]), record, exists)
        T.change(d, id, here, T.place(row, args[5]), score)
        count = count + 1
      end
    end
    return {'OK', count}
  end
  redis.register_function('ns_table_cell_add', T.write('cell_add', 6, function(d, args) return T.writecell(d, args, 'add') end))
  redis.register_function('ns_table_cell_remove', T.write('cell_remove', 5, function(d, args) return T.writecell(d, args, 'remove') end))
  redis.register_function('ns_table_cell_move', T.write('cell_move', 6, function(d, args) return T.writecell(d, args, 'move') end))
  redis.register_function('ns_table_member_create', T.write('member_create', 3, function(d, args)
    local record, exists, err = T.member(d, args[2])
    if err then return nil, err end
    if exists then return nil, T.refuse('MEMBEREXISTS', args[2]) end
    T.stage(d, 'HSET', T.memberkey(d, args[2]), 'epoch', d.epoch)
    T.change(d, args[2], nil, nil, nil)
    return {'OK'}
  end))
  redis.register_function('ns_table_bind', T.write('bind', 3, function(d, args, spec)
    if type(spec.rows) ~= 'table' then return nil, T.refuse('ROW') end
    local old, keep = redis.call('ZRANGE', T.rowskey(d), 0, -1), {}
    for i, row in ipairs(spec.rows) do
      if type(row) ~= 'table' or not T.word(row.key) then return nil, T.refuse('ROW') end
      if keep[row.key] then return nil, T.refuse('TWICE', row.key) end
      keep[row.key] = true
      local prior = T.hash(T.rowkey(d, row.key))
      local h, err = T.rowfields(d, row.key, row)
      if not h then return nil, err end
      local loss = T.keep_owned(d, row.key, prior, h)
      if loss then return nil, loss end
      T.stage(d, 'DEL', T.rowkey(d, row.key))
      T.hset(d.commands, T.rowkey(d, row.key), h)
      T.stage(d, 'ZADD', T.rowskey(d), i, row.key)
      for _, col in ipairs(d.cols) do d.cells[T.place(row.key, col.name)] = true end
    end
    for _, row in ipairs(old) do
      if not keep[row] then
        local prior = T.hash(T.rowkey(d, row))
        local loss = T.keep_owned(d, row, prior, nil)
        if loss then return nil, loss end
        local drift = T.remove(d, row, prior)
        if drift then return nil, drift end
      end
    end
    return {'OK'}
  end, 'bind'))
  function T.members(key, exclude)
    local values, kept = redis.call('ZRANGE', key, 0, -1, 'WITHSCORES'), {}
    for i = 1, #values, 2 do
      if exclude == '' or values[i] ~= exclude then kept[#kept + 1] = values[i]; kept[#kept + 1] = values[i + 1] end
    end
    return kept
  end
  redis.register_function{function_name = 'ns_table_members', flags={'no-writes'}, callback=function(keys, args)
    local d, err = T.def(args[1], args[4])
    if not d then return err end
    local cell, why = T.cell(d, args[2], args[3], false)
    if not cell then return why end
    return {'MEMBERS', T.members(cell.key, cell.exclude)}
  end}
  redis.register_function{function_name = 'ns_table_read', flags={'no-writes'}, callback=function(keys, args)
    local d, err = T.def(args[1], args[3])
    if not d then return err end
    local out = {'TABLE', T.flatdef(d), {}}
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      local h, flat = T.hash(T.rowkey(d, row))
      local cells = {}
      if args[2] ~= 'shape' then
        for _, col in ipairs(d.cols) do
          local value = {'OK', 0, {}}
          if col.projection ~= 'text' then
            local bound = h['key:' .. col.name]
            local key = bound and bound ~= '' and bound or T.cellkey(d, row, col.name)
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
  -- Maintenance check, never part of the mutation hot path. SCAN is exhausted
  -- inside this read-only function so both directions see one store instant.
  function T.scan(pattern, visit)
    local cursor = '0'
    repeat
      local batch = redis.call('SCAN', cursor, 'MATCH', pattern, 'COUNT', 1000)
      cursor = batch[1]
      for _, key in ipairs(batch[2]) do
        local err = visit(key)
        if err then return err end
      end
    until cursor == '0'
  end
  function T.pattern(prefix)
    local out = ''
    for i = 1, #prefix do
      local ch = string.sub(prefix, i, i)
      if ch == '*' or ch == '?' or ch == '[' or ch == ']' or ch == '\\' then out = out .. '\\' end
      out = out .. ch
    end
    return out .. '*'
  end
  redis.register_function{function_name = 'ns_table_check', flags={'no-writes'}, callback=function(keys, args)
    local d, err = T.def(args[1], args[2])
    if not d then return err end
    local seen, owned, members, cells = {}, {}, 0, 0
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      local h = T.hash(T.rowkey(d, row))
      for _, col in ipairs(d.cols) do
        local bound = h['key:' .. col.name]
        if bound and (bound == 'tables' or string.sub(bound, 1, 6) == 'table:') then return T.refuse('OWNEDALIAS', row, col.name, bound) end
        if col.projection ~= 'text' and (not bound or bound == '') then
          local key, place = T.cellkey(d, row, col.name), T.place(row, col.name)
          owned[key] = true; cells = cells + 1
          for _, id in ipairs(redis.call('ZRANGE', key, 0, -1)) do
            if seen[id] then return T.refuse('DRIFT', row, col.name, id, 'duplicate place') end
            local record, exists, why = T.member(d, id)
            if why then return why end
            if not exists or record['place:' .. d.name] ~= place then return T.refuse('DRIFT', row, col.name, id) end
            seen[id] = place; members = members + 1
          end
        end
      end
    end
    local ghost = T.scan(T.pattern(d.cfg.member_prefix), function(key)
      local record = T.hash(key)
      local place = record['place:' .. d.name]
      if place and (record.epoch or '0') == d.epoch then
        local id = string.sub(key, #d.cfg.member_prefix + 1)
        if seen[id] ~= place then return T.refuse('DRIFT', id, place, 'record has no owned cell') end
      end
    end)
    if ghost then return ghost end
    local hidden = T.scan(T.pattern(d.prefix .. ':cell:'), function(key)
      if not owned[key] and redis.call('ZCARD', key) ~= 0 then return T.refuse('DRIFT', key, 'hidden owned cell') end
    end)
    if hidden then return hidden end
    return {'CHECK', d.epoch, d.revision, tostring(members), tostring(cells)}
  end}
  redis.register_function{function_name = 'ns_table_list', flags={'no-writes'}, callback=function(keys, args)
    local names, out = redis.call('SMEMBERS', 'tables'), {'TABLES'}
    table.sort(names)
    for _, name in ipairs(names) do
      local d, err = T.open(name)
      if not d then return err end
      if d.present then out[#out + 1] = {name, #d.cols, redis.call('ZCARD', T.rowskey(d))} end
    end
    return out
  end}
end
