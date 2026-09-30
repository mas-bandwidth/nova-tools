-- Bounded, atomic table reads. The public registration in table_set.lua calls
-- S.read after all fragments have installed their helpers.
if NS.tset_profile then
  local S = NS.tset
  local MAX_RANK = '9007199254740991'
  local MAX_REPLY = 8388608
  local trusted_readcmd, begin_query, load_current_defs, read_row_cache
  -- Redis runs one Function callback at a time. Core clears this one private
  -- slot on context replacement and on every protected entrypoint exit.
  local private_ctx, probe_prefixes, encoded_ledger, private_defs,
    public_defs, private_active_epoch, private_space, private_request_epoch
  S.bind_read_helpers(function(readcmd, begin, load_defs, row_cache)
    trusted_readcmd = readcmd
    begin_query = begin
    load_current_defs = load_defs
    read_row_cache = row_cache
    return function()
      private_ctx, probe_prefixes, encoded_ledger = nil, nil, nil
      private_defs, public_defs, private_active_epoch = nil, nil, nil
      private_space, private_request_epoch = nil, nil
    end
  end)

  local function copy_value(value)
    if type(value) ~= 'table' then return value end
    local copied = S.is_array(value) and S.array() or {}
    for key, item in pairs(value) do copied[key] = copy_value(item) end
    return copied
  end

  local function same_value(current, saved, depth)
    if type(current) ~= type(saved) then return false end
    if type(saved) ~= 'table' then return current == saved end
    if depth > 8 or S.is_array(current) ~= S.is_array(saved) then return false end
    for key, item in pairs(saved) do
      if not same_value(current[key], item, depth + 1) then return false end
    end
    for key in pairs(current) do if saved[key] == nil then return false end end
    return true
  end

  local function defs_intact(ctx)
    if ctx ~= private_ctx or ctx.defs ~= public_defs or not private_defs then return false end
    for t, def in pairs(private_defs) do
      if not same_value(ctx.defs[t], def, 0) then return false end
    end
    for t in pairs(ctx.defs) do if not private_defs[t] then return false end end
    return true
  end

  local function table_prefix(t, epoch)
    return private_space .. 'table:' .. t ..
      (epoch == '0' and '' or ':' .. epoch)
  end

  local function rows_key(t, epoch)
    return table_prefix(t, epoch) .. ':rows'
  end

  local function cell_key(t, epoch, row, col)
    return table_prefix(t, epoch) .. ':cell:' .. row .. ':' .. col
  end

  local function ledger(ctx)
    if ctx ~= private_ctx then return nil end
    return encoded_ledger
  end

  local function empty()
    return S.array()
  end

  local function detail(index, t, id, cell)
    local d = {query_index = index, ids = empty(), cells = empty(), rows = empty()}
    if t then d.table = t end
    if id then d.ids[1] = id end
    if cell then d.cells[1] = cell end
    return d
  end

  local function fail(code, index, t, id, cell)
    return S.refuse(code, detail(index, t, id, cell))
  end

  local function checked(ctx, argv, key, kind, reserve, probe)
    return trusted_readcmd(ctx, {argv = argv,
      access = {{key = key, kind = kind, mode = 'read'}}}, reserve, probe)
  end

  local function charge(ctx, unit, count)
    if count == 0 then return true, nil end
    return S.charge(ctx, unit, count)
  end

  local function encoded_bytes(ctx, bytes, index)
    local state = ledger(ctx)
    if not state then return nil, fail('CONFIG', index) end
    if (ctx.read_encoded or 512) ~= state.bytes then return nil, fail('CONFIG', index) end
    local total = state.bytes + bytes + 1
    if total > MAX_REPLY then
      return nil, S.refuse('BUDGET', {query_index = index,
        ids = empty(), cells = empty(), rows = empty(),
        budget = 'encoded_reply', actual = total, limit = MAX_REPLY})
    end
    state.bytes = total
    ctx.read_encoded = total
    return true, nil
  end

  local function output(ctx, value, index)
    return encoded_bytes(ctx, #S.json.encode(value), index)
  end

  local function indexed(err, index)
    if err and type(err) == 'table' then
      err.detail = err.detail or {}
      if err.detail.query_index == nil then err.detail.query_index = index end
    end
    return err
  end

  local epoch_refusal = {STALE = true, EPOCHAHEAD = true,
    ADVANCE = true, EPOCHGONE = true}
  local function observed_epoch(err, ctx, index)
    err = indexed(err, index)
    if err and epoch_refusal[err.code] then err.detail.active_epoch = ctx.active_epoch end
    return err
  end

  -- Generic probes return only scalars or a fixed, caller-named field vector.
  -- Collection reads need a bounded helper that charges returned occurrences.
  local probe_commands = {
    HGET={'hash',3,3}, HMGET={'hash',3,2002}, HLEN={'hash',2,2},
    HSTRLEN={'hash',3,3}, HEXISTS={'hash',3,3},
    ZSCORE={'zset',3,3}, ZMSCORE={'zset',3,2002},
    ZCARD={'zset',2,2}, ZCOUNT={'zset',4,4}, XLEN={'stream',2,2},
    LLEN={'list',2,2}, LINDEX={'list',3,3},
    GET={'string',2,2}, STRLEN={'string',2,2},
    TYPE={'any',2,2}, EXISTS={'any',2,2}}

  local function structural_key(ctx, key)
    return key == ctx.space .. 'tables' or
      string.sub(key, 1, #ctx.space + 7) == ctx.space .. 'sprint:' or
      string.sub(key, 1, #ctx.space + 6) == ctx.space .. 'table:'
  end

  -- Definitions are a bounded catalog. A callback may name a table that was
  -- absent from the top-level query, so inspect every configured prefix once
  -- before a generic probe can touch a nonstructural key.
  local function is_member_key(ctx, key, index)
    if ctx ~= private_ctx then return nil, fail('CONFIG', index) end
    if structural_key(ctx, key) then return false, nil end
    local prefixes = probe_prefixes
    if not prefixes then
      local catalog_key = private_request_epoch == private_active_epoch and
        ctx.epoch_key or private_space .. 'sprint:epoch@' .. private_request_epoch
      local encoded, err = checked(ctx, {'HGET', catalog_key, 'tables'},
        catalog_key, 'hash', 2048, 'metadata')
      if err then return nil, indexed(err, index) end
      local valid, names = pcall(S.json.decode, encoded)
      if not valid or not S.is_array(names) or #names > 4 then
        return nil, fail('CONFIG', index)
      end
      prefixes = {}
      local seen = {}
      for _, t in ipairs(names) do
        if type(t) ~= 'string' or not string.match(t, '^[A-Za-z0-9_][A-Za-z0-9_.-]*$') or
            #t > 256 or seen[t] then return nil, fail('CONFIG', index) end
        seen[t] = true
        local def
        def, err = S.ensure_read_table(ctx, t, index)
        if err then return nil, err end
        prefixes[#prefixes + 1] = def.member_prefix
      end
      probe_prefixes = prefixes
    end
    for _, prefix in ipairs(prefixes) do
      if string.sub(key, 1, #prefix) == prefix then return true, nil end
    end
    return false, nil
  end

  function S.read_probe(ctx, argv, key, kind, reserve)
    local index = ctx and ctx.query_index
    if not ctx or ctx.operation ~= 'read' or type(index) ~= 'number' then
      return nil, fail('CONFIG', index)
    end
    if type(argv) ~= 'table' then return nil, fail('REQUEST', index) end
    local count, command = #argv, argv[1]
    local spec = type(command) == 'string' and probe_commands[command]
    if not spec or count < spec[2] or count > spec[3] or
        kind ~= spec[1] or type(key) ~= 'string' or #key == 0 or
        string.sub(key, 1, #ctx.space) ~= ctx.space or
        type(reserve) ~= 'number' or reserve ~= reserve or reserve == math.huge or
        reserve < 0 or reserve ~= math.floor(reserve) then
      return nil, fail('REQUEST', index)
    end
    for k, value in pairs(argv) do
      if type(k) ~= 'number' or k ~= math.floor(k) or k < 1 or k > count or
          type(value) ~= 'string' then return nil, fail('REQUEST', index) end
    end
    for i = 1, count do
      if type(argv[i]) ~= 'string' then return nil, fail('REQUEST', index) end
    end
    if argv[2] ~= key then
      return nil, fail('REQUEST', index)
    end
    local member, err = is_member_key(ctx, key, index)
    if err then return nil, err end
    if member then return nil, fail('CONFIG', index) end
    if command == 'HMGET' or command == 'ZMSCORE' then
      local ok
      ok, err = charge(ctx, 'cell', count - 3)
      if err then return nil, indexed(err, index) end
    end
    local value
    value, err = checked(ctx, argv, key, kind, reserve, 'cell')
    if err then return nil, indexed(err, index) end
    return value, nil
  end

  function S.read_range_head(ctx, key, bounds, limit)
    local index = ctx and ctx.query_index
    if not ctx or ctx.operation ~= 'read' or type(index) ~= 'number' then
      return nil, fail('CONFIG', index)
    end
    if type(bounds) ~= 'table' or type(key) ~= 'string' or #key == 0 or
        string.sub(key, 1, #ctx.space) ~= ctx.space or
        type(limit) ~= 'number' or limit ~= limit or limit == math.huge or
        limit ~= math.floor(limit) or limit < 1 then
      return nil, fail('REQUEST', index)
    end
    if limit > 2000 then return nil, fail('LIMIT', index) end
    for name in pairs(bounds) do
      if name ~= 'min' and name ~= 'max' and name ~= 'desc' then
        return nil, fail('REQUEST', index)
      end
    end
    if not S.score_bound(bounds.min) or not S.score_bound(bounds.max) or
        (bounds.desc ~= nil and type(bounds.desc) ~= 'boolean') then
      return nil, fail('REQUEST', index)
    end
    local argv = {'ZRANGE', key, bounds.desc and bounds.max or bounds.min,
      bounds.desc and bounds.min or bounds.max, 'BYSCORE'}
    if bounds.desc then argv[#argv + 1] = 'REV' end
    argv[#argv + 1] = 'LIMIT'
    argv[#argv + 1] = '0'
    argv[#argv + 1] = tostring(limit + 1)
    argv[#argv + 1] = 'WITHSCORES'
    local raw_pairs, err = checked(ctx, argv, key, 'zset',
      (limit + 1) * 320, 'cell')
    if err then return nil, indexed(err, index) end
    if type(raw_pairs) ~= 'table' or #raw_pairs % 2 ~= 0 or
        #raw_pairs > 2 * (limit + 1) then
      return nil, fail('DRIFT', index)
    end
    local size = #raw_pairs
    for position, value in pairs(raw_pairs) do
      if type(position) ~= 'number' or position ~= math.floor(position) or
          position < 1 or position > size or type(value) ~= 'string' then
        return nil, fail('DRIFT', index)
      end
    end
    for i = 1, size do
      if type(raw_pairs[i]) ~= 'string' then return nil, fail('DRIFT', index) end
    end
    local total = size / 2
    for i = 1, total do
      if not S.name(raw_pairs[2 * i - 1]) or
          type(raw_pairs[2 * i]) ~= 'string' then
        return nil, fail('DRIFT', index)
      end
    end
    local kept = math.min(total, limit)
    local ok
    ok, err = charge(ctx, 'range_id', kept)
    if err then return nil, indexed(err, index) end
    local ids, scores = empty(), empty()
    for i = 1, kept do
      ids[i], scores[i] = raw_pairs[2 * i - 1], raw_pairs[2 * i]
    end
    return {ids = ids, scores = scores, has_more = total > limit}, nil
  end

  -- A callback calls this before appending each growing nested item. Its
  -- estimated bytes are replaced by the exact answer size at query return.
  function S.emit_read_item(ctx, item, index)
    local state = ledger(ctx)
    local emitting = state and state.emit
    if not ctx or ctx.operation ~= 'read' or not emitting or
        ctx.read_emit ~= emitting.public or
        ctx.read_emit.index ~= emitting.index or
        ctx.read_emit.preceding ~= emitting.preceding or
        index ~= ctx.query_index or index ~= emitting.index then
      return nil, fail('CONFIG', index)
    end
    if type(item) ~= 'table' then return nil, fail('CONFIG', index) end
    local ok, encoded = pcall(S.json.encode, item)
    if not ok or type(encoded) ~= 'string' then return nil, fail('CONFIG', index) end
    return encoded_bytes(ctx, #encoded, index)
  end

  -- Composite readers discover table names after open_state has examined the
  -- top-level queries. A retained read must never fall back to today's hash.
  function S.ensure_read_table(ctx, t, index)
    if not ctx or ctx.operation ~= 'read' or type(t) ~= 'string' or #t > 256 or
        not string.match(t, '^[A-Za-z0-9_][A-Za-z0-9_.-]*$') then
      return nil, fail('REQUEST', index, t)
    end
    if not defs_intact(ctx) then return nil, fail('CONFIG', index, t) end
    local def = private_defs[t]
    if def then return copy_value(def), nil end
    if private_request_epoch == private_active_epoch then
      local ok, err = load_current_defs(ctx, t)
      if err then return nil, err end
      for name, loaded in pairs(ctx.defs) do
        if not private_defs[name] then private_defs[name] = copy_value(loaded) end
      end
      def = private_defs[t]
      if not def then return nil, fail('NOTABLE', index, t) end
      if not defs_intact(ctx) then return nil, fail('CONFIG', index, t) end
      return copy_value(def), nil
    end
    local total = 0
    for _ in pairs(private_defs) do total = total + 1 end
    if total >= 4 then
      return nil, S.refuse('LIMIT', {query_index = index, table = t,
        ids = empty(), cells = empty(), rows = empty(),
        budget = 'tables', actual = total + 1, limit = 4})
    end
    local key = table_prefix(t, private_request_epoch) .. ':definition'
    local raw, err = checked(ctx, {'HGETALL', key}, key, 'hash', 49152, 'metadata')
    if err then return nil, err end
    if #raw == 0 then
      return nil, S.refuse('EPOCHGONE', {query_index = index, table = t,
        active_epoch = ctx.active_epoch, ids = empty(), cells = empty(), rows = empty()})
    end
    local values = {}
    for i = 1, #raw, 2 do values[raw[i]] = raw[i + 1] end
    def, err = S.definition(ctx, t, values)
    if err then return nil, err end
    ctx.defs[t] = def
    private_defs[t] = copy_value(def)
    return copy_value(private_defs[t]), nil
  end

  local function table_def(ctx, t, index)
    return S.ensure_read_table(ctx, t, index)
  end

  local function rank_valid(v)
    return type(v) == 'string' and
      (v == '0' or string.match(v, '^[1-9][0-9]*$')) and
      (#v < #MAX_RANK or (#v == #MAX_RANK and v <= MAX_RANK))
  end

  local function cell(ctx, t, name, index)
    local def, err = table_def(ctx, t, index)
    if err then return nil, err end
    local row, col = S.cell(name)
    if not row or not def.column_set[col] then
      return nil, fail(not row and 'REQUEST' or 'NOCOL', index, t, nil, name)
    end
    local key = rows_key(t, private_request_epoch)
    local row_scores
    row_scores, err = read_row_cache(ctx)
    if err then return nil, err end
    local cached = row_scores[key]
    if not cached then cached = {}; row_scores[key] = cached end
    local rank = cached[row]
    if rank == nil then
      rank, err = checked(ctx, {'ZSCORE', key, row}, key, 'zset', 32, 'cell')
      if err then return nil, err end
      cached[row] = rank
    end
    if rank == false or rank == nil then return nil, fail('NOROW', index, t, nil, name) end
    if not rank_valid(rank) then return nil, fail('DRIFT', index, t, nil, name) end
    return cell_key(t, private_request_epoch, row, col), nil
  end

  local function record(ctx, t, id, requested, index)
    local b, fields, err
    if requested == nil then
      b, fields, err = S.before_whole(ctx, t, id)
    else
      fields = requested
      local before
      before, err = S.before(ctx, t, {id}, fields)
      if not err then b = before[id] end
    end
    if err then return nil, err end
    if not b then return nil, fail('DRIFT', index, t, id) end
    local values = {}
    for _, field in ipairs(fields) do
      local v = b.fields and b.fields[field]
      if not v then return nil, fail('DRIFT', index, t, id) end
      values[field] = {present = v.present, value = v.present and v.value or cjson.null}
    end
    local out = {id = id, exists = b.exists, epoch = b.epoch or cjson.null,
      revision = b.revision or cjson.null, place = b.place or cjson.null,
      score = b.score or cjson.null, fields = values}
    return out, nil
  end

  -- Sprint composite projections require an explicit field array. This keeps
  -- each occurrence charged while S.before fetches each payload only once.
  function S.read_record(ctx, t, id, fields, index)
    if not ctx or ctx.operation ~= 'read' or not S.name(id) then
      return nil, fail('REQUEST', index, t, id)
    end
    if type(fields) ~= 'table' or #fields > 128 then
      return nil, fail(type(fields) == 'table' and 'LIMIT' or 'REQUEST', index, t, id)
    end
    local length = #fields
    local selected, seen, repeated = empty(), {}, 0
    for k, field in pairs(fields) do
      if type(k) ~= 'number' or k ~= math.floor(k) or k < 1 or k > length or
          not S.name(field) then
        return nil, fail('REQUEST', index, t, id)
      end
      if field == 'epoch' or field == 'revision' or string.sub(field, 1, 6) == 'place:' then
        return nil, fail('FIELDNAME', index, t, id)
      end
    end
    for i = 1, length do
      if fields[i] == nil then return nil, fail('REQUEST', index, t, id) end
      if seen[fields[i]] then
        repeated = repeated + 1
      else
        seen[fields[i]] = true
        selected[#selected + 1] = fields[i]
      end
    end
    local _, err = S.ensure_read_table(ctx, t, index)
    if err then return nil, err end
    -- A duplicate projected name is still an occurrence, but one context
    -- fetch and one object field suffice for its value.
    local ok
    ok, err = charge(ctx, 'field', repeated)
    if err then return nil, err end
    return record(ctx, t, id, selected, index)
  end

  local function range(ctx, q, index)
    local key, err
    if q.key then
      local prefix = ctx.space
      if string.sub(q.key, 1, #prefix) ~= prefix then
        return nil, fail('REQUEST', index)
      end
      key = q.key
    else
      key, err = cell(ctx, q.t, q.cell, index)
      if err then return nil, err end
    end
    local bounds = q.desc and {q.max, q.min} or {q.min, q.max}
    local argv = {'ZRANGE', key, bounds[1], bounds[2], 'BYSCORE'}
    if q.desc then argv[#argv + 1] = 'REV' end
    argv[#argv + 1] = 'LIMIT'
    argv[#argv + 1] = '0'
    argv[#argv + 1] = tostring(q.limit + 1)
    argv[#argv + 1] = 'WITHSCORES'
    local pairs
    pairs, err = checked(ctx, argv, key, 'zset',
      (q.limit + 1) * 320, 'cell')
    if err then return nil, err end
    if #pairs % 2 ~= 0 then return nil, fail('DRIFT', index, q.t) end
    local total = #pairs / 2
    -- Validate even the lookahead member. Raw-key ranges share the same
    -- supported-writer identifier invariant as table-cell ranges.
    for n = 1, total do
      if not S.name(pairs[2 * n - 1]) then
        return nil, fail('DRIFT', index, q.t)
      end
    end
    local kept = math.min(total, q.limit)
    local ok
    ok, err = charge(ctx, 'range_id', kept)
    if err then return nil, err end
    local ids, scores = empty(), empty()
    for n = 1, kept do
      local id, score = pairs[2 * n - 1], pairs[2 * n]
      ids[n], scores[n] = id, score
    end
    local answer = {kind = 'range', ids = ids, scores = scores,
      has_more = total > q.limit}
    local accounted
    accounted, err = output(ctx, answer, index)
    if err then return nil, err end
    if q.records then
      local records = empty()
      for n, id in ipairs(ids) do
        records[n], err = record(ctx, q.t, id, q.fields, index)
        if err then return nil, err end
        if records[n].place == cjson.null then
          return nil, fail('DRIFT', index, q.t, id, q.cell)
        end
        local row, col = S.cell(q.cell)
        if records[n].place.row ~= row or records[n].place.col ~= col or
            records[n].score ~= scores[n] then
          return nil, fail('DRIFT', index, q.t, id, q.cell)
        end
        accounted, err = output(ctx, records[n], index)
        if err then return nil, err end
      end
      answer.records = records
    end
    return answer, nil
  end

  local function counts(ctx, q, index)
    local counts_out, sum = empty(), 0
    for n, name in ipairs(q.cells) do
      local key, err = cell(ctx, q.t, name, index)
      if err then return nil, err end
      local value
      if q.kind == 'count' then
        value, err = checked(ctx, {'ZCARD', key}, key, 'zset', 32, 'cell')
      else
        value, err = checked(ctx, {'ZCOUNT', key, q.min, q.max}, key, 'zset', 32, 'cell')
      end
      if err then return nil, err end
      if type(value) ~= 'number' or value < 0 or value > 9007199254740991 then
        return nil, fail('DRIFT', index, q.t, nil, name)
      end
      counts_out[n] = value
      sum = sum + value
      if sum > 9007199254740991 then return nil, fail('OVERFLOW', index, q.t) end
    end
    local answer = {kind = q.kind, counts = counts_out, sum = sum}
    local ok, err = output(ctx, answer, index)
    if err then return nil, err end
    return answer, nil
  end

  local function ids(ctx, q, index)
    local _, err = table_def(ctx, q.t, index)
    if err then return nil, err end
    local out = empty()
    for n, id in ipairs(q.ids) do
      out[n], err = record(ctx, q.t, id, q.fields, index)
      if err then return nil, err end
      local ok
      ok, err = output(ctx, out[n], index)
      if err then return nil, err end
    end
    return {kind = 'ids', records = out}, nil
  end

  local function rows(ctx, q, index)
    local _, err = table_def(ctx, q.t, index)
    if err then return nil, err end
    local key = rows_key(q.t, private_request_epoch)
    local count
    count, err = checked(ctx, {'ZCARD', key}, key, 'zset', 32, 'cell')
    if err then return nil, err end
    if type(count) ~= 'number' or count < 0 or count ~= math.floor(count) then
      return nil, fail('DRIFT', index, q.t)
    end
    local maximum = count * 280
    local fetched = ctx.budget.fetched_bytes + maximum
    if fetched > S.limits.fetched_bytes then
      return nil, S.refuse('BUDGET', {query_index = index,
        budget = 'fetched_bytes', actual = fetched, limit = S.limits.fetched_bytes})
    end
    local encoded = (ctx.read_encoded or 512) + maximum
    if encoded > MAX_REPLY then
      return nil, S.refuse('BUDGET', {query_index = index,
        budget = 'encoded_reply', actual = encoded, limit = MAX_REPLY})
    end
    local out, offset = empty(), 0
    while offset < count do
      local page = math.min(256, count - offset)
      local values
      values, err = checked(ctx, {'ZRANGE', key, tostring(offset),
        tostring(offset + page - 1), 'WITHSCORES'}, key, 'zset', page * 280, 'cell')
      if err then return nil, err end
      if #values ~= 2 * page then return nil, fail('DRIFT', index, q.t) end
      local n = #values / 2
      for i = 1, n do
        local row, rank = values[2 * i - 1], values[2 * i]
        if not S.name(row) or not rank_valid(rank) then
          return nil, fail('DRIFT', index, q.t)
        end
        out[#out + 1] = {row = row, rank = rank}
        local ok
        ok, err = output(ctx, out[#out], index)
        if err then return nil, err end
      end
      offset = offset + n
    end
    return {kind = 'rows', rows = out}, nil
  end

  local function answer(ctx, q, index, log_reader, extension, extension_kinds)
    if q.kind == 'range' then return range(ctx, q, index) end
    if q.kind == 'count' or q.kind == 'rcount' then return counts(ctx, q, index) end
    if q.kind == 'ids' then return ids(ctx, q, index) end
    if q.kind == 'rows' then return rows(ctx, q, index) end
    if q.kind == 'done' then
      local slots, err = S.done_read(ctx, q.ops)
      if err then return nil, err end
      for _, slot in ipairs(slots) do
        local ok
        ok, err = output(ctx, slot, index)
        if err then return nil, err end
      end
      return {kind = 'done', slots = slots}, nil
    end
    if q.kind == 'last' or q.kind == 'lines' or q.kind == 'cardlines' then
      if not log_reader then return nil, fail('REQUEST', index) end
      local result, err = log_reader(ctx, q)
      if err then return nil, err end
      local ok
      ok, err = output(ctx, result, index)
      if err then return nil, err end
      return result, nil
    end
    if extension_kinds and extension_kinds[q.kind] then
      local state = ledger(ctx)
      if not state then return nil, fail('CONFIG', index) end
      local preceding = state.bytes
      if (ctx.read_encoded or 512) ~= preceding then return nil, fail('CONFIG', index) end
      local public_emit = {index = index, preceding = preceding}
      state.emit = {public = public_emit, index = index, preceding = preceding}
      ctx.read_emit = public_emit
      local budget = ctx.budget
      local prior = {}
      for name, value in pairs(budget) do
        if type(value) == 'number' then prior[name] = value end
      end
      local result, err = S.read_callback(ctx, extension.read, q, index)
      local valid_emit = ctx.read_emit == public_emit and
        public_emit.index == index and public_emit.preceding == preceding and
        (ctx.read_encoded or 512) == state.bytes
      ctx.read_emit = nil
      state.emit = nil
      if not valid_emit then return nil, fail('CONFIG', index) end
      if not defs_intact(ctx) then return nil, fail('CONFIG', index) end
      if ctx.budget ~= budget then return nil, fail('CONFIG', index) end
      for name, value in pairs(prior) do
        if type(budget[name]) ~= 'number' or budget[name] < value then
          return nil, fail('CONFIG', index)
        end
      end
      if err then
        if type(err) ~= 'table' or err.status ~= 'refused' or
            type(err.code) ~= 'string' or type(err.detail) ~= 'table' then
          return nil, fail('CONFIG', index)
        end
        return nil, observed_epoch(err, ctx, index)
      end
      if type(result) ~= 'table' then return nil, fail('CONFIG', index) end
      local ok, encoded = pcall(S.json.encode, result)
      if not ok or type(encoded) ~= 'string' then return nil, fail('CONFIG', index) end
      -- Emitted nested items were charged before append. Replace only this
      -- query's estimate by its exact complete answer, avoiding double charge.
      state.bytes = preceding
      ctx.read_encoded = preceding
      ok, err = encoded_bytes(ctx, #encoded, index)
      if err then return nil, err end
      return result, nil
    end
    return nil, fail('REQUEST', index)
  end

  local function read_inner(version, raw_plan, log_reader, extension)
    local request, err = S.validate(version, raw_plan, 'read', extension)
    if err then return S.json.encode(err) end
    if not log_reader then
      for n, q in ipairs(request.queries) do
        if q.kind == 'last' or q.kind == 'lines' or q.kind == 'cardlines' then
          return S.json.encode(fail('REQUEST', n - 1))
        end
      end
    end
    local extension_kinds
    if extension then
      extension_kinds, err = S.extension_kinds(extension)
      if err then return S.json.encode(err) end
    end
    local ctx
    ctx, err = S.context(request, 'read')
    if err then return S.json.encode(err) end
    private_ctx, probe_prefixes, encoded_ledger = ctx, nil, {bytes = 512}
    private_space, private_request_epoch = ctx.space, ctx.request_epoch
    local opened
    opened, err = S.open_state(ctx)
    if err then return S.json.encode(err) end
    public_defs, private_defs, private_active_epoch = ctx.defs, {}, ctx.active_epoch
    for t, def in pairs(ctx.defs) do private_defs[t] = copy_value(def) end
    local now
    now, err = S.readcmd(ctx, {argv = {'TIME'}, access = {}}, 64, 'metadata')
    if err then return S.json.encode(err) end
    ctx.now_ms = now[1] .. string.format('%03d', math.floor(tonumber(now[2]) / 1000))
    local answers = empty()
    for n, q in ipairs(request.queries) do
      local result
      local begun
      begun, err = begin_query(ctx, n - 1)
      if err then return S.json.encode(observed_epoch(err, ctx, n - 1)) end
      if not begun then return S.json.encode(fail('CONFIG', n - 1)) end
      result, err = answer(ctx, q, n - 1, log_reader, extension, extension_kinds)
      if err then
        return S.json.encode(observed_epoch(err, ctx, n - 1))
      end
      answers[n] = result
    end
    local reply = {status = 'read', epoch = request.epoch,
      active_epoch = ctx.active_epoch, time_ms = ctx.now_ms,
      answers = answers, complete = true, counters = ctx.budget}
    if request.mode == 'page' then
      -- Only Layer 2 reads support pages; its single answer is a prepared page.
      reply = answers[1]
      reply.active_epoch = ctx.active_epoch
      reply.time_ms = ctx.now_ms
      reply.counters = ctx.budget
    end
    local encoded = S.json.encode(reply)
    if #encoded > MAX_REPLY then
      return S.json.encode(S.refuse('BUDGET', {query_index = ctx.query_index,
        ids = empty(), cells = empty(), rows = empty(),
        budget = 'encoded_reply', limit = MAX_REPLY,
        actual = #encoded}))
    end
    return encoded
  end
  function S.read(version, raw_plan, log_reader, extension)
    return S.run_context(read_inner, version, raw_plan, log_reader, extension)
  end
end
