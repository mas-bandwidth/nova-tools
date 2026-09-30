-- Layer 1's lifecycle: ns_tset_define and ns_tset_teardown (the L1 contract
-- amendment, lifecycle, 2026-09-30). Each call is atomic: every refusal is
-- decided and every command is checked against the caller's ACL before the
-- first write. table_set.lua registers both callbacks; they resolve here.
if NS.tset_profile then
  local S = NS.tset
  local SYMBOLIC = '^[A-Za-z0-9_][A-Za-z0-9_.-]*$'
  local MAX_TABLES = 4            -- the catalog's bound (L1 1.2)
  local MAX_COLUMNS = 32          -- a definition's bound (L1 1.2)
  local REQUEST_BYTES = 65536     -- a lifecycle request's encoded bound
  local TEARDOWN_KEYS = 1000      -- keys one teardown call deletes at most (F3: pieces <= 1,000)
  local TEARDOWN_SCANS = 16       -- SCAN calls one teardown call makes at most
  local SCAN_COUNT = '1000'
  local RECEIPTS_MAX = '1024'     -- the receipt stream's length bound

  local function refuse(code, detail)
    return S.json.encode(S.refuse(code, detail))
  end

  local function symbolic(s)
    return type(s) == 'string' and #s > 0 and #s <= 256 and string.match(s, SYMBOLIC) ~= nil
  end

  -- The keys the lifecycle names, all under the space (L1 1.2; the amendment,
  -- section 1).
  local function keys_of(space)
    return {
      epoch = space .. 'sprint:epoch',
      epoch0 = space .. 'sprint:epoch@0',
      view = space .. 'sprint:view',
      receipts = space .. 'sprint:lifecycle',
      clock = space .. 'sprint:clock',
      table = function(t) return space .. 'table:' .. t end,
      definition = function(t) return space .. 'table:' .. t .. ':definition' end,
      member = function(t) return space .. 'member:' .. t .. ':' end,
    }
  end

  -- decode admits exactly the named fields, each a JSON string unless listed
  -- in structured.
  local function decode(version, raw, fields)
    if version ~= 'tset/1' then return nil, 'VERSION' end
    if type(raw) ~= 'string' then return nil, 'REQUEST' end
    if #raw > REQUEST_BYTES then return nil, 'LIMIT' end
    if not S.utf8_valid(raw) then return nil, 'REQUEST' end
    local ok, req = pcall(S.json.decode, raw)
    if not ok or not S.is_object(req) then return nil, 'REQUEST' end
    for k, v in pairs(req) do
      local want = fields[k]
      if not want then return nil, 'REQUEST' end
      if want == 'string' and type(v) ~= 'string' then return nil, 'REQUEST' end
    end
    return req, nil
  end

  local function allowed(argv)
    return redis.acl_check_cmd(unpack(argv))
  end

  -- read runs one read command after its ACL check; a denied read is NOPERM
  -- with nothing written.
  local function read(argv)
    if not allowed(argv) then return nil, 'NOPERM' end
    return redis.call(unpack(argv)), nil
  end

  local function key_type(key)
    local t, code = read({'TYPE', key})
    if code then return nil, code end
    return type(t) == 'table' and t.ok or t, nil
  end

  local function now_ms()
    local t, code = read({'TIME'})
    if code then return nil, code end
    return t[1] .. string.format('%03d', math.floor(tonumber(t[2]) / 1000)), nil
  end

  local function glob_literal(s)
    return (string.gsub(s, '[%*%?%[%]\\]', '\\%0'))
  end

  -- commit checks every planned write against the caller's ACL, then runs
  -- them: a refusal writes nothing (F1).
  local function commit(writes)
    for _, argv in ipairs(writes) do
      if not allowed(argv) then return 'NOPERM' end
    end
    for _, argv in ipairs(writes) do redis.call(unpack(argv)) end
    return nil
  end

  -- ns_tset_define: the space, its tables with their columns and kinds, the
  -- view, the engine record and the active epoch at 0, exactly the keys the
  -- trusted fixture initializer writes (L1 1.2), and the view and a receipt
  -- (the amendment, section 2).
  function S.lifecycle_define(keys, args)
    if #keys ~= 0 or #args ~= 2 then return refuse('ARGS') end
    local req, code = decode(args[1], args[2], {space = 'string', build = 'string', view = 'string', tables = 'array'})
    if code then return refuse(code) end
    if type(req.build) ~= 'string' then return refuse('REQUEST') end
    local space = req.space
    if not S.name(space) or not symbolic(req.view) or not S.is_array(req.tables) or #req.tables < 1 then
      return refuse('REQUEST')
    end
    if #req.tables > MAX_TABLES then
      return refuse('LIMIT', {budget = 'tables', actual = #req.tables, limit = MAX_TABLES})
    end
    local names, seen, defs = {}, {}, {}
    for i, t in ipairs(req.tables) do
      if not S.is_object(t) then return refuse('REQUEST', {entry_index = i - 1}) end
      for k in pairs(t) do
        if k ~= 't' and k ~= 'columns' then return refuse('REQUEST', {entry_index = i - 1}) end
      end
      if not symbolic(t.t) or seen[t.t] or not S.is_array(t.columns) or #t.columns < 1 then
        return refuse('REQUEST', {entry_index = i - 1})
      end
      if #t.columns > MAX_COLUMNS then
        return refuse('LIMIT', {entry_index = i - 1, table = t.t, budget = 'columns', actual = #t.columns, limit = MAX_COLUMNS})
      end
      local columns, cseen = {}, {}
      for _, c in ipairs(t.columns) do
        if not S.is_object(c) then return refuse('REQUEST', {entry_index = i - 1, table = t.t}) end
        for k in pairs(c) do
          if k ~= 'name' and k ~= 'kind' then return refuse('REQUEST', {entry_index = i - 1, table = t.t}) end
        end
        if not symbolic(c.name) or cseen[c.name] or type(c.kind) ~= 'string' then
          return refuse('REQUEST', {entry_index = i - 1, table = t.t})
        end
        -- Only a set column: the twin models no derived kind (the amendment,
        -- section 2).
        if c.kind ~= 'set' then return refuse('CONFIG', {entry_index = i - 1, table = t.t}) end
        cseen[c.name] = true
        columns[#columns + 1] = c.name
      end
      seen[t.t] = true
      names[#names + 1] = t.t
      defs[#defs + 1] = {t = t.t, columns = columns}
    end
    -- BUILD: this library is not the build the caller names.
    if req.build ~= NS.tset_build then return refuse('BUILD') end
    local k = keys_of(space)
    -- EXISTS: the space already holds a definition, a marker or a view.
    local probe = {'EXISTS', k.epoch, k.epoch0, k.view}
    for _, name in ipairs(names) do
      probe[#probe + 1] = k.table(name)
      probe[#probe + 1] = k.definition(name)
    end
    local present
    present, code = read(probe)
    if code then return refuse(code) end
    if present ~= 0 then return refuse('EXISTS') end
    local rtype
    rtype, code = key_type(k.receipts)
    if code then return refuse(code) end
    if rtype ~= 'none' and rtype ~= 'stream' then return refuse('WRONGTYPE') end
    local now
    now, code = now_ms()
    if code then return refuse(code) end
    local catalog = S.json.encode(names)
    local writes = {}
    for _, d in ipairs(defs) do
      local fields = {'engine', 'tset/1', 'order', table.concat(d.columns, ','),
        'member_prefix', k.member(d.t), 'epoch_key', k.epoch, 'epoch_field', 'n'}
      for _, c in ipairs(d.columns) do
        fields[#fields + 1] = 'col:' .. c
        fields[#fields + 1] = 'set'
      end
      local live, snapshot = {'HSET', k.table(d.t)}, {'HSET', k.definition(d.t)}
      for _, f in ipairs(fields) do
        live[#live + 1] = f
        snapshot[#snapshot + 1] = f
      end
      writes[#writes + 1] = live
      writes[#writes + 1] = snapshot
    end
    writes[#writes + 1] = {'HSET', k.epoch, 'engine', 'tset/1', 'n', '0', 'tables', catalog}
    writes[#writes + 1] = {'HSET', k.epoch0, 'engine', 'tset/1', 'n', '0', 'tables', catalog}
    writes[#writes + 1] = {'HSET', k.view, 'engine', 'tset/1', 'name', req.view, 'tables', catalog}
    writes[#writes + 1] = {'XADD', k.receipts, 'MAXLEN', RECEIPTS_MAX, '*',
      'fn', 'define', 'time_ms', now, 'view', req.view, 'tables', catalog, 'build', req.build}
    code = commit(writes)
    if code then return refuse(code) end
    return S.json.encode({status = 'ok', space = space, view = req.view, tables = names,
      epoch = '0', time_ms = now})
  end

  -- ns_tset_teardown: one bounded batch of the space's keys, under the view's
  -- name, refused while the machine runs (the amendment, section 3). The pass
  -- state lives in the view, which goes last: the call that finds a whole
  -- SCAN pass with nothing left deletes it and answers done.
  function S.lifecycle_teardown(keys, args)
    if #keys ~= 0 or #args ~= 2 then return refuse('ARGS') end
    local req, code = decode(args[1], args[2], {space = 'string', confirm = 'string'})
    if code then return refuse(code) end
    local space = req.space
    if not S.name(space) or type(req.confirm) ~= 'string' then return refuse('REQUEST') end
    local k = keys_of(space)
    local vtype
    vtype, code = key_type(k.view)
    if code then return refuse(code) end
    if vtype == 'none' then return refuse('NOSPACE') end
    if vtype ~= 'hash' then return refuse('WRONGTYPE') end
    local view
    view, code = read({'HMGET', k.view, 'name', 'teardown_cursor', 'teardown_dirty'})
    if code then return refuse(code) end
    if view[1] ~= req.confirm then return refuse('CONFIRM') end
    -- RUNNING is the upper design's clock (1.2): the clock exists and has no
    -- STOPPED span open.
    local ctype
    ctype, code = key_type(k.clock)
    if code then return refuse(code) end
    if ctype == 'hash' then
      local since
      since, code = read({'HGET', k.clock, 'stopped_since_ms'})
      if code then return refuse(code) end
      if not since or since == '' or since == '0' then return refuse('RUNNING') end
    elseif ctype ~= 'none' then
      return refuse('WRONGTYPE')
    end
    local rtype
    rtype, code = key_type(k.receipts)
    if code then return refuse(code) end
    if rtype ~= 'none' and rtype ~= 'stream' then return refuse('WRONGTYPE') end
    local cursor, dirty = view[2] or '0', view[3] or '0'
    if not S.uint(cursor) or (dirty ~= '0' and dirty ~= '1') then return refuse('CONFIG') end
    local pattern = glob_literal(space) .. '*'
    local batch, inbatch, done = {'DEL'}, {}, false
    for _ = 1, TEARDOWN_SCANS do
      local page
      page, code = read({'SCAN', cursor, 'MATCH', pattern, 'COUNT', SCAN_COUNT})
      if code then return refuse(code) end
      local nextc, fresh = page[1], {}
      for _, key in ipairs(page[2]) do
        if key ~= k.view and key ~= k.receipts and not inbatch[key] then fresh[#fresh + 1] = key end
      end
      local room = TEARDOWN_KEYS - (#batch - 1)
      for j = 1, math.min(#fresh, room) do
        inbatch[fresh[j]] = true
        batch[#batch + 1] = fresh[j]
      end
      if #fresh > 0 then dirty = '1' end
      -- A full batch leaves the cursor where it is: the rest of the page is
      -- found again by the next call.
      if #fresh > room or #batch - 1 == TEARDOWN_KEYS then break end
      if nextc == '0' then
        if dirty == '0' then done = true; break end
        cursor, dirty = '0', '0'
      else
        cursor = nextc
      end
    end
    local deleted = #batch - 1
    local now
    now, code = now_ms()
    if code then return refuse(code) end
    local writes = {}
    if deleted > 0 then writes[#writes + 1] = batch end
    if done then
      writes[#writes + 1] = {'DEL', k.view}
    else
      writes[#writes + 1] = {'HSET', k.view, 'teardown_cursor', cursor, 'teardown_dirty', dirty}
    end
    if deleted > 0 or done then
      writes[#writes + 1] = {'XADD', k.receipts, 'MAXLEN', RECEIPTS_MAX, '*',
        'fn', 'teardown', 'time_ms', now, 'view', req.confirm, 'deleted', tostring(deleted), 'done', done and '1' or '0'}
    end
    code = commit(writes)
    if code then return refuse(code) end
    return S.json.encode({status = 'ok', deleted = deleted, done = done, time_ms = now})
  end
end
