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
  function T.shape(h, repair)
    if type(h) ~= 'table' or type(h.order) ~= 'string' then return nil, T.refuse('DEFINITION') end
    local cols, seen, order = {}, {}, {}
    for k, v in pairs(h) do
      if type(k) ~= 'string' or type(v) ~= 'string' or
          not (k == 'order' or k == 'footer' or k == 'created_at' or k == 'epoch_key' or
          k == 'epoch_field' or k == 'member_prefix' or k == 'hidden' or k == 'visible' or k == 'sort' or string.sub(k, 1, 4) == 'col:') then
        return nil, T.refuse('DEFINITION', tostring(k))
      end
    end
    for col in string.gmatch(h.order, '[^,]+') do
      local proj, fold, width = string.match(h['col:' .. col] or '', '^([^:]+):([^:]+):([^:]+):')
      local valid = {count=true,members=true,first=true,last=true,text=true}
      local formula = proj and string.match(proj, '^pct%([%w_.-]+%)$') ~= nil
      local w = tonumber(width)
      if not T.name(col) or not (valid[proj or ''] or formula) or seen[col] or not T.uint(width) or #width > 19 or
        (#width == 19 and width > '9223372036854775807') or not w or w < 0 or w ~= math.floor(w) or
        not (repair or fold == 'none' or ((fold == 'sum' or fold == 'max') and proj == 'count') or
          (fold == 'avg' and proj == 'count') or (fold == 'pooled' and formula) or
          (fold == 'union' and proj ~= 'count' and proj ~= 'text' and not formula)) then
        return nil, T.refuse('DEFINITION', col)
      end
      seen[col] = true
      order[#order + 1] = col
      cols[#cols + 1] = {name=col, projection=proj, noset=proj == 'text' or formula}
    end
    if #cols == 0 or table.concat(order, ',') ~= h.order then return nil, T.refuse('DEFINITION') end
    if h.sort and not (string.match(h.sort, '^-?name$') or string.match(h.sort, '^-?label$')) then return nil, T.refuse('DEFINITION', 'sort') end
    for _, col in ipairs(cols) do
      local arg = string.match(col.projection, '^pct%(([%w_.-]+)%)$')
      if arg and (not seen[arg] or not string.match(h['col:' .. arg] or '', '^count:')) then return nil, T.refuse('DEFINITION', col.name) end
    end
    if not repair then
      local hidden = {}
      for col in string.gmatch(h.hidden or '', '[^,]+') do
        if not seen[col] then return nil, T.refuse('NOCOL', '', col) end
        hidden[#hidden+1] = col
      end
      if table.concat(hidden, ',') ~= (h.hidden or '') or (h.visible and h.visible ~= '0' and h.visible ~= '1') then return nil, T.refuse('DEFINITION') end
    end
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
  function T.open(name, fields, historical, repair)
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
    local cols, err = T.shape(h, repair)
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
    if c.noset then return nil, T.refuse('TEXT', row, col) end
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
      if c.noset then return nil, T.refuse('TEXT', row, col) end
      if not T.word(key) then return nil, T.refuse('BINDKEY', row, col) end
      -- Reserve the entire table namespace, including future epochs/metadata.
      if key == 'tables' or string.sub(key, 1, 6) == 'table:' then return nil, T.refuse('OWNEDALIAS', row, col, key) end
      h['key:' .. col] = key
    end
    return h
  end
  function T.keep_text(d, old, new)
    if old.hidden then new.hidden = old.hidden end
    for _, col in ipairs(d.cols) do
      local field = 'text:' .. col.name
      if col.projection == 'text' and old[field] ~= nil then new[field] = old[field] end
    end
  end
  function T.sortedkeys(h)
    local keys = {}
    for key in pairs(h) do keys[#keys + 1] = key end
    table.sort(keys)
    return keys
  end
  function T.keep_owned(d, row, old, new)
    for _, col in ipairs(d.cols) do
      local prior, target = old['key:' .. col.name], new and new['key:' .. col.name]
      if (prior and prior ~= '') or col.noset then
        local hidden = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
        if #hidden > 0 then return T.refuse('OCCUPIED', row, col.name, hidden) end
      end
      if not col.noset and (not prior or prior == '') and (not new or (target and target ~= '')) then
        local members = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
        if #members > 0 then return T.refuse('OCCUPIED', row, col.name, members) end
      end
    end
  end
  function T.remove(d, row, h)
    for _, col in ipairs(d.cols) do
      d.cells[T.place(row, col.name)] = true
      local bound = h['key:' .. col.name]
      if (bound and bound ~= '') or col.noset then
        local hidden = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
        if #hidden > 0 then return T.refuse('OCCUPIED', row, col.name, hidden) end
      end
      if not col.noset and (not h['key:' .. col.name] or h['key:' .. col.name] == '') then
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
  -- Order is state (Glenn 2026-09-27: "take column y and put it after column
  -- z", "friends on top, machines on bottom"). T.reorder is the one move: the
  -- item leaves the list and enters at first, last, before or after a
  -- reference; a permutation, nothing added, nothing lost (tla/TableOrder.tla).
  function T.split(s)
    local list = {}
    for item in string.gmatch(s or '', '[^,]+') do list[#list + 1] = item end
    return list
  end
  function T.reorder(list, item, where, ref, missing)
    local out, found, at = {}, false, nil
    for _, v in ipairs(list) do if v == item then found = true else out[#out + 1] = v end end
    if not found then return nil, missing(item) end
    if where == 'first' then at = 1
    elseif where == 'last' then at = #out + 1
    elseif where == 'before' or where == 'after' then
      if ref == item then return nil, T.refuse('SELF', tostring(item)) end
      for i, v in ipairs(out) do if v == ref then at = where == 'before' and i or i + 1 end end
      if not at then return nil, missing(ref) end
    else return nil, T.refuse('WHERE', tostring(where)) end
    table.insert(out, at, item)
    return out
  end
  function T.norow(row) return T.refuse('NOROW', tostring(row)) end
  function T.nocol(col) return T.refuse('NOCOL', '', tostring(col)) end
  -- T.sorted(d, by, desc): the table's rows (and the rows this call adds or
  -- rewrites, d.touched) in the order of name, label, or a column's value (a
  -- count, or a text value); ties by name, so the result is one order only.
  function T.sorted(d, by, desc)
    local rows, seen, keys = {}, {}, {}
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      if not (d.removed and d.removed[row]) then rows[#rows + 1] = row; seen[row] = true end
    end
    for _, row in ipairs(T.sortedkeys(d.touched or {})) do
      if not seen[row] then rows[#rows + 1] = row; seen[row] = true end
    end
    local col
    if by ~= 'name' and by ~= 'label' then
      col = T.col(d, by)
      if not col then return nil, T.nocol(by) end
      if col.noset and col.projection ~= 'text' then return nil, T.refuse('SORTKEY', by) end
    end
    for _, row in ipairs(rows) do
      local h = (d.touched and d.touched[row]) or T.hash(T.rowkey(d, row))
      if by == 'name' then keys[row] = string.lower(row)
      elseif by == 'label' then keys[row] = string.lower((h.label and h.label ~= '') and h.label or row)
      elseif col.projection == 'text' then keys[row] = string.lower(h['text:' .. by] or '')
      else
        local bound = h['key:' .. by]
        local key = (bound and bound ~= '') and bound or T.cellkey(d, row, by)
        local n = redis.call('ZCARD', key)
        if h.exclude and h.exclude ~= '' and redis.call('ZSCORE', key, h.exclude) then n = n - 1 end
        keys[row] = n
      end
    end
    table.sort(rows, function(a, b)
      if keys[a] ~= keys[b] then
        if desc then return keys[a] > keys[b] end
        return keys[a] < keys[b]
      end
      return a < b
    end)
    return rows
  end
  -- T.rank(d, rows): the rows' order written as ranks 1..n, in one ZADD,
  -- only when it differs from the order the store holds.
  function T.rank(d, rows)
    local have = redis.call('ZRANGE', T.rowskey(d), 0, -1)
    local same = #have == #rows
    for i, row in ipairs(rows) do if have[i] ~= row then same = false end end
    if same or #rows == 0 then return end
    local cmd = {'ZADD', T.rowskey(d)}
    for i, row in ipairs(rows) do cmd[#cmd + 1] = tostring(i); cmd[#cmd + 1] = row end
    d.commands[#d.commands + 1] = cmd
  end
  -- Untrimmed change streams are the receipt. All validation (including
  -- stream/revision bounds and ACLs) precedes all writes; XADD is last.
  function T.finish(d, verb, args, opts, reply)
    local after = T.next(d.revision)
    if not after then return T.refuse('REVISION', d.revision) end
    local stream = d.key .. ':changes'
    local source_stream = d.receipt_source or stream
    local kind = redis.call('TYPE', source_stream).ok
    if kind ~= 'none' and kind ~= 'stream' then return T.refuse('STREAMTYPE', stream) end
    if kind == 'stream' then
      local info = redis.call('XINFO', 'STREAM', source_stream)
      for i = 1, #info, 2 do
        if info[i] == 'last-generated-id' and info[i + 1] == '18446744073709551615-18446744073709551615' then
          return T.refuse('STREAMFULL', stream)
        end
      end
    end
    -- a standing sort (set by row sort --keep) places the rows this call
    -- added or relabelled; the rank in the store is always the order drawn
    if d.h.sort and d.touched then
      local by, desc = string.match(d.h.sort, '^-?(%a+)$'), string.sub(d.h.sort, 1, 1) == '-'
      local rows, why = T.sorted(d, by, desc)
      if not rows then return why end
      T.rank(d, rows)
    end
    local outcome = #d.commands == 0 and not d.newtemplate and d.snap._present ~= '0' and 'noop' or 'changed'
    if d.newtemplate then
      redis.call('SCARD', 'tables')
      T.hset(d.commands, d.key, d.h)
      T.stage(d, 'SADD', 'tables', d.name)
    end
    if d.definition_changed then
      T.stage(d, 'DEL', d.key)
      T.hset(d.commands, d.key, d.h)
    end
    if d.newidentity then T.hset(d.commands, d.key .. ':identity', d.cfg) end
    -- Materialised epochs retain their own definition after template removal.
    if d.newtemplate or d.definition_changed then T.stage(d, 'DEL', d.prefix .. ':definition') end
    if d.newtemplate or d.definition_changed or not d.snap.order then T.hset(d.commands, d.prefix .. ':definition', d.h) end
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
    if d.renamed_keys then event[#event + 1] = 'renamed_keys'; event[#event + 1] = cjson.encode(d.renamed_keys) end
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
      if (argc >= 0 and #args ~= argc) or (argc < 0 and #args < -argc) then return T.refuse('ARGS', verb) end
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
      local edit = verb == 'set' and T.decode(args[2])
      local d, err = T.open(args[1], fields, nil, edit and edit.columns ~= nil)
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
  function T.addrow(d, row, spec)
    local h, err = T.rowfields(d, row, spec)
    if not h then return nil, err end
    local old = T.hash(T.rowkey(d, row))
    T.keep_text(d, old, h)
    local loss = T.keep_owned(d, row, old, h)
    if loss then return nil, loss end
    local rank = redis.call('ZSCORE', T.rowskey(d), row)
    if not rank then
      if not d.tailrank then
        local tail = redis.call('ZRANGE', T.rowskey(d), -1, -1, 'WITHSCORES')
        d.tailrank = tonumber(tail[2]) or 0
      end
      local n = d.tailrank
      if n ~= n or n + 1 == n or n == math.huge then return nil, T.refuse('RANK') end
      d.tailrank, rank = n + 1, tostring(n + 1)
    end
    T.stage(d, 'DEL', T.rowkey(d, row))
    T.hset(d.commands, T.rowkey(d, row), h)
    T.stage(d, 'ZADD', T.rowskey(d), rank, row)
    for _, col in ipairs(d.cols) do d.cells[T.place(row, col.name)] = true end
    d.touched = d.touched or {}
    d.touched[row] = h
    return h
  end
  function T.list(values)
    if type(values) ~= 'table' or #values == 0 then return nil end
    local seen, count = {}, 0
    for key, value in pairs(values) do
      if type(key) ~= 'number' or key < 1 or key > #values or key ~= math.floor(key) or not T.word(value) or seen[value] then return nil end
      seen[value], count = true, count + 1
    end
    return count == #values
  end
  redis.register_function('ns_table_row_add', T.write('row_add', 4, function(d, args)
    local h, err = T.addrow(d, args[2], T.decode(args[3]))
    if not h then return nil, err end
    return function() return {'ROW', T.flatdef(d), T.flat(h)} end
  end))
  redis.register_function('ns_table_rows_add', T.write('rows_add', 3, function(d, args)
    local spec = T.decode(args[2])
    if not spec or not T.list(spec.rows) or type(spec.spec) ~= 'table' then return nil, T.refuse('ROW') end
    for _, row in ipairs(spec.rows) do
      local h, err = T.addrow(d, row, spec.spec)
      if not h then return nil, err end
    end
    return {'OK', #spec.rows}
  end))
  redis.register_function('ns_table_rows_hide', T.write('rows_hide', 4, function(d, args)
    local rows, flag = T.decode(args[3]), args[2]
    if not T.list(rows) or (flag ~= '0' and flag ~= '1') then return nil, T.refuse('ROW') end
    for _, row in ipairs(rows) do
      if not redis.call('ZSCORE', T.rowskey(d), row) then return nil, T.refuse('NOROW', row) end
      local h = T.hash(T.rowkey(d, row))
      if flag == '1' and h.hidden ~= '1' then T.stage(d, 'HSET', T.rowkey(d, row), 'hidden', '1') end
      if flag == '0' and h.hidden then T.stage(d, 'HDEL', T.rowkey(d, row), 'hidden') end
      for _, col in ipairs(d.cols) do d.cells[T.place(row, col.name)] = true end
    end
    return {'OK', #rows}
  end))
  -- Scalar edits and definition changes use the same observed-epoch,
  -- staged-command and receipt protocol as every member mutation.
  redis.register_function('ns_table_row_set', T.write('row_set', 4, function(d, args)
    local row, values = args[2], T.decode(args[3])
    if not T.word(row) or type(values) ~= 'table' or not next(values) then return nil, T.refuse('ROW') end
    if not redis.call('ZSCORE', T.rowskey(d), row) then return nil, T.refuse('NOROW', row) end
    local h = T.hash(T.rowkey(d, row))
    for col, value in pairs(values) do
      if type(col) ~= 'string' or type(value) ~= 'string' then return nil, T.refuse('ROW', row) end
      local c = T.col(d, col)
      if not c then return nil, T.refuse('NOCOL', row, col) end
      if c.projection ~= 'text' then return nil, T.refuse('NOTTEXT', row, col) end
    end
    local columns = T.sortedkeys(values)
    for _, col in ipairs(columns) do
      if h['text:' .. col] ~= values[col] then
        T.stage(d, 'HSET', T.rowkey(d, row), 'text:' .. col, values[col])
        d.cells[T.place(row, col)] = true
      end
    end
    return {'OK', #columns}
  end))
  function T.reshape(d, cols)
    local kept, oldcols = {}, {}
    for _, col in ipairs(cols) do kept[col.name] = col end
    for _, col in ipairs(d.cols) do oldcols[col.name] = col end
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      local h = T.hash(T.rowkey(d, row))
      for _, col in ipairs(d.cols) do
        local replacement = kept[col.name]
        local bound = h['key:' .. col.name]
        -- Never hide, erase, or resurrect physical owned members via shape.
        if col.noset or (bound and bound ~= '') or not replacement or replacement.noset then
          local members = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
          if #members > 0 then return T.refuse('OCCUPIED', row, col.name, members) end
        end
        if col.projection == 'text' and (not replacement or replacement.projection ~= 'text') then
          if h['text:' .. col.name] and h['text:' .. col.name] ~= '' then return T.refuse('OCCUPIEDVALUE', row, col.name) end
          T.stage(d, 'HDEL', T.rowkey(d, row), 'text:' .. col.name)
        end
        if not replacement or replacement.noset then T.stage(d, 'HDEL', T.rowkey(d, row), 'key:' .. col.name) end
        d.cells[T.place(row, col.name)] = true
      end
      for _, col in ipairs(cols) do
        if not oldcols[col.name] then
          local members = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
          if #members > 0 then return T.refuse('OCCUPIED', row, col.name, members) end
          T.stage(d, 'HDEL', T.rowkey(d, row), 'text:' .. col.name, 'key:' .. col.name)
        end
        d.cells[T.place(row, col.name)] = true
      end
    end
  end
  function T.rename(d, name)
    if not T.name(name) then return nil, T.refuse('NAME', name) end
    if name == d.name then return 0 end
    local dest = 'table:' .. name
    if redis.call('EXISTS', dest) ~= 0 then return nil, T.refuse('EXISTS', name) end
    local collision = T.scan(T.pattern(dest .. ':'), function() return T.refuse('EXISTS', name) end)
    if collision then return nil, collision end
    redis.call('SCARD', 'tables')
    local keys, records = {[d.key]=true}, {}
    T.scan(T.pattern(d.key .. ':'), function(key) keys[key] = true end)
    -- SCAN may repeat keys; collect before staging so RENAME executes once.
    T.scan(T.pattern(d.cfg.member_prefix), function(key)
      local record = T.hash(key)
      if record['place:' .. d.name] then records[key] = record end
    end)
    local edits = d.commands
    d.commands = {}
    for _, key in ipairs(T.sortedkeys(records)) do
      local record = records[key]
      local id, at, epoch = string.sub(key, #d.cfg.member_prefix + 1), record['place:' .. d.name], record.epoch or '0'
      local row, col = string.match(at, '^(.+):([^:]+)$')
      if not row or not T.uint(epoch) or record['place:' .. name] then return nil, T.refuse('DRIFT', id, at) end
      local score = redis.call('ZSCORE', T.prefix(d.name, epoch) .. ':cell:' .. row .. ':' .. col, id)
      if not score then return nil, T.refuse('DRIFT', id, at) end
      T.stage(d, 'HSET', key, 'place:' .. name, at)
      T.stage(d, 'HDEL', key, 'place:' .. d.name)
      d.members[#d.members + 1] = {id=id, from=at, to=at, score=score, epoch=epoch, from_table=d.name, to_table=name}
      d.cells[at] = true
    end
    local sources = T.sortedkeys(keys)
    d.renamed_keys = {}
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      for _, col in ipairs(d.cols) do d.cells[T.place(row, col.name)] = true end
    end
    for _, key in ipairs(sources) do
      local target = dest .. string.sub(key, #d.key + 1)
      T.stage(d, 'RENAME', key, target)
      d.renamed_keys[#d.renamed_keys + 1] = {from=key, to=target}
    end
    -- Rename before deleting row fields: HDEL can remove the last field and
    -- hence the source key. Retarget the already validated edits afterward.
    for _, cmd in ipairs(edits) do
      if cmd[2] == d.key or string.sub(cmd[2], 1, #d.key + 1) == d.key .. ':' then
        cmd[2] = dest .. string.sub(cmd[2], #d.key + 1)
      end
      d.commands[#d.commands + 1] = cmd
    end
    T.stage(d, 'SREM', 'tables', d.name)
    T.stage(d, 'SADD', 'tables', name)
    d.receipt_source = d.key .. ':changes'
    d.name, d.key, d.prefix = name, dest, T.prefix(name, d.epoch)
    return #sources
  end
  redis.register_function('ns_table_set', T.write('set', 3, function(d, args)
    local spec = T.decode(args[2])
    if type(spec) ~= 'table' or not next(spec) then return nil, T.refuse('DEFINITION') end
    for key in pairs(spec) do
      if key ~= 'footer' and key ~= 'columns' and key ~= 'rename' and key ~= 'hidden' and key ~= 'hide' and key ~= 'show' and key ~= 'visible' and
          key ~= 'col_add' and key ~= 'col_del' and key ~= 'col_move' and key ~= 'row_move' and key ~= 'row_order' and key ~= 'row_sort' then return nil, T.refuse('BADSET', key) end
    end
    if spec.footer ~= nil and type(spec.footer) ~= 'string' then return nil, T.refuse('DEFINITION') end
    if spec.rename ~= nil and not T.name(spec.rename) then return nil, T.refuse('NAME') end
    local h = {}
    for key, value in pairs(d.h) do h[key] = value end
    if spec.columns ~= nil then
      if type(spec.columns) ~= 'table' then return nil, T.refuse('DEFINITION') end
      for key in pairs(h) do if key == 'order' or string.sub(key, 1, 4) == 'col:' then h[key] = nil end end
      for key, value in pairs(spec.columns) do
        if type(key) ~= 'string' or (key ~= 'order' and string.sub(key, 1, 4) ~= 'col:') then return nil, T.refuse('DEFINITION') end
        h[key] = value
      end
    end
    if spec.footer ~= nil then h.footer = spec.footer end
    if spec.hidden ~= nil then
      if type(spec.hidden) ~= 'string' then return nil, T.refuse('DEFINITION') end
      h.hidden = spec.hidden
    end
    if spec.visible ~= nil then
      if type(spec.visible) ~= 'boolean' then return nil, T.refuse('DEFINITION') end
      h.visible = spec.visible and '1' or '0'
    end
    if spec.hide ~= nil or spec.show ~= nil then
      local hidden, list = {}, {}
      for col in string.gmatch(h.hidden or '', '[^,]+') do hidden[col] = true end
      for _, change in ipairs({'hide', 'show'}) do
        if spec[change] ~= nil then
          if not T.list(spec[change]) then return nil, T.refuse('DEFINITION') end
          for _, col in ipairs(spec[change]) do
            if not h['col:' .. col] then return nil, T.refuse('NOCOL', '', col) end
            hidden[col] = change == 'hide'
          end
        end
      end
      for col in string.gmatch(h.order or '', '[^,]+') do if hidden[col] then list[#list+1] = col end end
      h.hidden = table.concat(list, ',')
    end
    -- one column added, removed or moved; the rest of the definition stands
    if spec.col_add ~= nil then
      local a = spec.col_add
      if type(a) ~= 'table' or not T.name(a.name) or type(a.def) ~= 'string' then return nil, T.refuse('DEFINITION') end
      if h['col:' .. a.name] then return nil, T.refuse('COLEXISTS', a.name) end
      local list = T.split(h.order)
      list[#list + 1] = a.name
      if a.where ~= nil then
        local moved, why = T.reorder(list, a.name, a.where, a.ref, T.nocol)
        if not moved then return nil, why end
        list = moved
      end
      h['col:' .. a.name], h.order = a.def, table.concat(list, ',')
    end
    if spec.col_del ~= nil then
      local gone = spec.col_del
      if type(gone) ~= 'string' or not h['col:' .. gone] then return nil, T.nocol(gone) end
      local list, hidden = {}, {}
      for _, col in ipairs(T.split(h.order)) do
        if col ~= gone then
          list[#list + 1] = col
          if string.match(h['col:' .. col] or '', '^pct%(([%w_.-]+)%)') == gone then return nil, T.refuse('DEPENDS', gone, col) end
        end
      end
      if #list == 0 then return nil, T.refuse('LASTCOL', gone) end
      for _, col in ipairs(T.split(h.hidden)) do if col ~= gone then hidden[#hidden + 1] = col end end
      h['col:' .. gone], h.order = nil, table.concat(list, ',')
      if h.hidden ~= nil then h.hidden = table.concat(hidden, ',') end
    end
    if spec.col_move ~= nil then
      local m = spec.col_move
      if type(m) ~= 'table' or type(m.col) ~= 'string' then return nil, T.refuse('DEFINITION') end
      local list, why = T.reorder(T.split(h.order), m.col, m.where, m.ref, T.nocol)
      if not list then return nil, why end
      h.order = table.concat(list, ',')
    end
    -- the rows' order: one row moved, some rows named first, or all sorted
    local rows
    if (spec.row_move ~= nil or spec.row_order ~= nil) and h.sort and spec.row_sort == nil then
      return nil, T.refuse('SORTED', h.sort)
    end
    if spec.row_sort ~= nil then
      local o = spec.row_sort
      if type(o) ~= 'table' then return nil, T.refuse('DEFINITION') end
      if o.manual then h.sort = nil
      else
        if type(o.by) ~= 'string' or o.by == '' then return nil, T.refuse('SORTKEY', tostring(o.by)) end
        if o.keep and o.by ~= 'name' and o.by ~= 'label' then return nil, T.refuse('SORTKEEP', o.by) end
        local why
        rows, why = T.sorted(d, o.by, o.desc and true or false)
        if not rows then return nil, why end
        if o.keep then h.sort = (o.desc and '-' or '') .. o.by else h.sort = nil end
      end
    end
    if spec.row_order ~= nil then
      if not T.list(spec.row_order) then return nil, T.refuse('ROW') end
      local named = {}
      for _, row in ipairs(spec.row_order) do named[row] = true end
      local have, out = rows or redis.call('ZRANGE', T.rowskey(d), 0, -1), {}
      local present = {}
      for _, row in ipairs(have) do present[row] = true end
      for _, row in ipairs(spec.row_order) do
        if not present[row] then return nil, T.norow(row) end
        out[#out + 1] = row
      end
      for _, row in ipairs(have) do if not named[row] then out[#out + 1] = row end end
      rows = out
    end
    if spec.row_move ~= nil then
      local m = spec.row_move
      if type(m) ~= 'table' or type(m.row) ~= 'string' then return nil, T.refuse('ROW') end
      local why
      rows, why = T.reorder(rows or redis.call('ZRANGE', T.rowskey(d), 0, -1), m.row, m.where, m.ref, T.norow)
      if not rows then return nil, why end
    end
    local cols, err = T.shape(h)
    if not cols then return nil, err end
    if spec.columns or spec.col_add or spec.col_del then
      local loss = T.reshape(d, cols)
      if loss then return nil, loss end
    end
    if rows then T.rank(d, rows) end
    local moved = 0
    if spec.rename then
      local count, problem = T.rename(d, spec.rename)
      if count == nil then return nil, problem end
      moved = count
    end
    local function differs(a, b)
      for k, v in pairs(a) do if b[k] ~= v then return true end end
      for k in pairs(b) do if a[k] == nil then return true end end
      return false
    end
    d.definition_changed = spec.footer ~= nil or spec.columns ~= nil or spec.hidden ~= nil or spec.visible ~= nil or spec.hide ~= nil or spec.show ~= nil or
      ((spec.col_add ~= nil or spec.col_del ~= nil or spec.col_move ~= nil or spec.row_sort ~= nil) and differs(d.h, h))
    d.h, d.cols = h, cols
    return {'OK', moved}
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
    local row, col = args[2], args[3]
    local src, err = T.cell(d, row, col, true)
    if not src then return nil, err end
    local count, first, dst = redis.call('ZCARD', src.key), 5, nil
    if op == 'add' then
      local score = tonumber(args[4])
      if not score or score ~= score or score == math.huge or score == -math.huge then return nil, T.refuse('SCORE') end
    elseif op == 'remove' then first = 4
    else
      dst, err = T.cell(d, row, args[4], true)
      if not dst then return nil, err end
      count = redis.call('ZCARD', dst.key)
    end
    local seen, here = {}, T.place(row, col)
    for i = first, #args - 1 do
      local id = args[i]
      if seen[id] then return nil, T.refuse('TWICE', id) end
      seen[id] = true
      local record, exists, why = T.member(d, id)
      if why then return nil, why end
      local placed = record['place:' .. d.name]
      if op == 'add' then
        if placed then return nil, T.refuse('PLACED', placed, id) end
        local drift = T.unindexed(d, id)
        if drift then return nil, drift end
        T.stage(d, 'ZADD', src.key, args[4], id)
        T.record(d, id, here, record, exists)
        T.change(d, id, nil, here, args[4])
        count = count + 1
      elseif op == 'remove' then
        local score = redis.call('ZSCORE', src.key, id)
        if placed ~= here then
          if score then return nil, T.refuse('DRIFT', row, col, id) end
        else
          if not score then return nil, T.refuse('DRIFT', row, col, id) end
          T.stage(d, 'ZREM', src.key, id)
          T.record(d, id, nil, record, exists)
          T.change(d, id, here, nil, score)
          count = count - 1
        end
      else
        local score = redis.call('ZSCORE', src.key, id)
        if not score then return nil, T.refuse('NOTMEMBER', row, col, id) end
        if placed ~= here then return nil, T.refuse('DRIFT', row, col, id) end
        if dst.key ~= src.key and redis.call('ZSCORE', dst.key, id) then return nil, T.refuse('DRIFT', row, args[4], id) end
        if dst.key ~= src.key then
          T.stage(d, 'ZREM', src.key, id)
          T.stage(d, 'ZADD', dst.key, score, id)
          T.record(d, id, T.place(row, args[4]), record, exists)
          T.change(d, id, here, T.place(row, args[4]), score)
          count = count + 1
        end
      end
    end
    return {'OK', count}
  end
  redis.register_function('ns_table_cell_add', T.write('cell_add', -6, function(d, args) return T.writecell(d, args, 'add') end))
  redis.register_function('ns_table_cell_remove', T.write('cell_remove', -5, function(d, args) return T.writecell(d, args, 'remove') end))
  redis.register_function('ns_table_cell_move', T.write('cell_move', -6, function(d, args) return T.writecell(d, args, 'move') end))
  -- Views are presentation configuration, separate from a table's epoch and ledger.
  -- Validate every referenced table and every command before publishing the view.
  redis.register_function('ns_view_set', function(keys, args)
    if #args ~= 4 or not T.name(args[1]) then return T.refuse('ARGS', 'view_set') end
    local names = {}
    for name in string.gmatch(args[2], '[^,]+') do
      local d, err = T.def(name)
      if not d then return err end
      if #names == 0 and args[4] ~= '' then
        local summary = T.col(d, args[4])
        if not summary or summary.projection ~= 'count' then return T.refuse('NOCOL', '', args[4]) end
      end
      names[#names+1] = name
    end
    if #names == 0 or table.concat(names, ',') ~= args[2] then return T.refuse('ARGS', 'tables') end
    local key = 'view:' .. args[1]
    T.hash(key)
    redis.call('SCARD', 'views')
    local commands = {{'HSET', key, 'tables', args[2], 'title', args[3], 'summary', args[4]}, {'SADD', 'views', args[1]}}
    for _, cmd in ipairs(commands) do if not redis.acl_check_cmd(unpack(cmd)) then return T.refuse('NOPERM', cmd[1], cmd[2]) end end
    for _, cmd in ipairs(commands) do redis.call(unpack(cmd)) end
    return {'OK'}
  end)
  redis.register_function{function_name = 'ns_view_get', flags = {'no-writes'}, callback = function(keys, args)
    if #args ~= 1 or not T.name(args[1]) then return T.refuse('ARGS', 'view_get') end
    local h = redis.call('HGETALL', 'view:' .. args[1])
    if #h == 0 then return T.refuse('NOVIEW', args[1]) end
    return {'OK', h}
  end}
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
      T.keep_text(d, prior, h)
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
          if not col.noset then
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
        if not col.noset and (not bound or bound == '') then
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
