-- Row topology for ns_tset_step. This fragment runs after table_set.lua and
-- installs only planning helpers: all Redis writes remain in S.commit.
if NS.tset_profile then
  local S = NS.tset
  local MAX_RANK = '9007199254740991'

  local function read(ctx, argv, key, reserve)
    return S.readcmd(ctx, {
      argv = argv,
      access = {{key = key, kind = 'zset', mode = 'read'}},
    }, reserve, 'cell')
  end

  local function row_detail(t, row, index)
    return {entry_index = index, table = t, ids = {}, cells = {}, rows = {row}}
  end

  local function rank_valid(rank)
    return type(rank) == 'string' and
      (rank == '0' or string.match(rank, '^[1-9][0-9]*$')) and
      (#rank < #MAX_RANK or (#rank == #MAX_RANK and rank <= MAX_RANK))
  end

  -- Observe only rows named in the request. The table can have arbitrarily
  -- many other rows, so no ZRANGE of the complete row index is permitted.
  local function observe(ctx, topo, t, row)
    local by_table = topo.observed[t]
    if not by_table then by_table = {}; topo.observed[t] = by_table end
    local cached = by_table[row]
    if cached then return cached, nil end
    local key = ctx.rows_key(t, ctx.write_epoch)
    -- S.before may already have established this exact row score while
    -- checking an indicated member placement. Reuse its observation rather
    -- than charging a second row probe during the subsequent table plan.
    local shared = ctx.row_scores[key]
    if not shared then shared = {}; ctx.row_scores[key] = shared end
    local score = shared[row]
    if score == nil then
      local err
      score, err = read(ctx, {'ZSCORE', key, row}, key, 32)
      if err then return nil, err end
      shared[row] = score
    end
    local advances = ctx.write_epoch ~= ctx.request_epoch
    if advances and score ~= false and score ~= nil then
      return nil, S.refuse('DRIFT', row_detail(t, row))
    end
    if score ~= false and score ~= nil and not rank_valid(score) then
      return nil, S.refuse('DRIFT', row_detail(t, row))
    end
    cached = {present = score ~= false and score ~= nil, rank = score}
    by_table[row] = cached
    return cached, nil
  end

  -- Collect every row name before any row or member command is planned. A
  -- repeated direction belongs to its first entry; opposite directions are
  -- forbidden even when the row's pre-state would make one a no-op.
  function S.rows_collect(ctx)
    local topo = {observed = {}, by_table = {}, order = {}, add_order = {},
      del_order = {}, count = 0}
    local entries = ctx.request.entries or {}
    for n, entry in ipairs(entries) do
      if entry.kind == 'rows' then
        local t = entry.t
        local rows = topo.by_table[t]
        if not rows then rows = {}; topo.by_table[t] = rows end
        for _, direction in ipairs({'add', 'del'}) do
          for _, row in ipairs(entry[direction] or {}) do
            local item = rows[row]
            if not item then
              topo.count = topo.count + 1
              item = {table = t, row = row}
              rows[row] = item
              topo.order[#topo.order + 1] = item
            end
            if item.other and item.other ~= direction then
              return nil, S.refuse('ROWCONFLICT', row_detail(t, row, n - 1))
            end
            item.other = direction
            if not item[direction] then
              item[direction] = n - 1
              if direction == 'add' then
                topo.add_order[#topo.add_order + 1] = item
              else
                topo.del_order[#topo.del_order + 1] = item
              end
            end
          end
        end
      end
    end
    local limit = ctx.write_epoch ~= ctx.request_epoch and 1024 or 100
    if topo.count > limit then
      return nil, S.refuse('LIMIT', {
        ids = {}, cells = {}, rows = {}, budget = 'rows', limit = limit,
        actual = topo.count,
      })
    end
    for _, item in ipairs(topo.order) do
      local observed, err = observe(ctx, topo, item.table, item.row)
      if err then return nil, err end
      item.pre = observed.present
      item.pre_rank = observed.rank
    end
    return topo, nil
  end

  -- Sources and count/rcount guards refer to pre-state. Destinations refer to
  -- final topology, after every add and deletion in the entire step.
  function S.rows_require(ctx, topo, t, row, role, entry_index)
    local item = topo.by_table[t] and topo.by_table[t][row]
    local observed, err = observe(ctx, topo, t, row)
    if err then return nil, err end
    if role == 'destination' then
      if item and item.del ~= nil then
        return nil, S.refuse('ROWCONFLICT', row_detail(t, row, entry_index))
      end
      if observed.present or (item and item.add ~= nil) then return true, nil end
      return nil, S.refuse('NOROW', row_detail(t, row, entry_index))
    end
    if role == 'source' or role == 'count' then
      if observed.present then return true, nil end
      return nil, S.refuse('NOROW', row_detail(t, row, entry_index))
    end
    return nil, S.refuse('REQUEST', row_detail(t, row, entry_index))
  end

  local function max_rank(ctx, t)
    local key = ctx.rows_key(t, ctx.write_epoch)
    local last, err = read(ctx, {'ZREVRANGE', key, '0', '0', 'WITHSCORES'}, key, 288)
    if err then return nil, err end
    if ctx.write_epoch ~= ctx.request_epoch and #last ~= 0 then
      return nil, S.refuse('DRIFT', {table = t, ids = {}, cells = {}, rows = {}})
    end
    if #last == 0 then return nil, nil end
    if #last ~= 2 or not rank_valid(last[2]) then
      return nil, S.refuse('DRIFT', {table = t, ids = {}, cells = {}, rows = {}})
    end
    return last[2], nil
  end

  local function write(ctx, plan, argv, key)
    local descriptor, err = S.writecmd(ctx, argv,
      {{key = key, kind = 'zset', mode = 'write'}})
    if err then return nil, err end
    plan.commands[#plan.commands + 1] = descriptor
    return true, nil
  end

  local function flush(ctx, plan, key, argv)
    if #argv <= 2 then return true, nil end
    return write(ctx, plan, argv, key)
  end

  -- This runs after member planning. Deleting a row is legal when its final
  -- occupancy is zero, including when the same step removes its last member.
  -- Each check is one named row x one defined column; it never scans a cell.
  function S.rows_plan(ctx, topo, plan)
    for n, entry in ipairs(ctx.request.entries or {}) do
      if entry.kind == 'rows' then
        plan.entries[n] = {kind = 'rows', entry_index = n - 1,
          table = entry.t, added = {}, deleted = {}}
      end
    end

    for _, item in ipairs(topo.del_order) do
      if item.pre then
        local t, row = item.table, item.row
        for _, col in ipairs(ctx.defs[t].columns) do
          local key = ctx.cell_key(t, ctx.write_epoch, row, col)
          local incoming = ctx.cell_incoming and ctx.cell_incoming[key]
          if incoming == true or (type(incoming) == 'number' and incoming > 0) then
            return nil, S.refuse('ROWCONFLICT', row_detail(t, row, item.del))
          end
          local count, err = read(ctx, {'ZCARD', key}, key, 32)
          if err then return nil, err end
          local final = count + ((ctx.cell_deltas and ctx.cell_deltas[key]) or 0)
          if final ~= 0 then
            return nil, S.refuse('OCCUPIED', row_detail(t, row, item.del))
          end
        end
        plan.entries[item.del + 1].deleted[#plan.entries[item.del + 1].deleted + 1] = row
      end
    end

    local next_rank = {}
    for _, item in ipairs(topo.add_order) do
      if not item.pre then
        local t = item.table
        if next_rank[t] == nil then
          local last, err = max_rank(ctx, t)
          if err then return nil, err end
          next_rank[t] = last or '-1'
        end
        local rank
        if next_rank[t] == '-1' then rank = '0'
        else rank = S.next(next_rank[t]) end
        if not rank or not rank_valid(rank) then
          return nil, S.refuse('OVERFLOW', row_detail(t, item.row, item.add))
        end
        next_rank[t] = rank
        item.rank = rank
        plan.entries[item.add + 1].added[#plan.entries[item.add + 1].added + 1] =
          {row = item.row, rank = rank}
      end
    end

    -- Keep a complete aligned entry array, but expose only changed topology
    -- entries to Layer 2. A row entry with no effective change emits no line.
    for n = 1, #(ctx.request.entries or {}) do
      local event = plan.entries[n]
      if event and event.kind == 'rows' and
          (#event.added > 0 or #event.deleted > 0) then
        plan.rows[#plan.rows + 1] = event
      end
    end

    -- Member commands have already been appended, so row removals execute
    -- after every outgoing ZREM. Batch each table's row mutations within the
    -- same 1,000-element command-piece bound as the common registry.
    local function batch(direction, order)
      local by_table, tables = {}, {}
      for _, item in ipairs(order) do
        if (direction == 'del' and item.pre) or
            (direction == 'add' and not item.pre) then
          local t = item.table
          if not by_table[t] then by_table[t] = {}; tables[#tables + 1] = t end
          by_table[t][#by_table[t] + 1] = item
        end
      end
      for _, t in ipairs(tables) do
        local key = ctx.rows_key(t, ctx.write_epoch)
        local argv = direction == 'del' and {'ZREM', key} or {'ZADD', key}
        local pieces = 0
        for _, item in ipairs(by_table[t]) do
          if pieces == 1000 then
            local ok, err = flush(ctx, plan, key, argv)
            if err then return nil, err end
            argv = direction == 'del' and {'ZREM', key} or {'ZADD', key}
            pieces = 0
          end
          if direction == 'del' then
            argv[#argv + 1] = item.row
          else
            argv[#argv + 1] = item.rank
            argv[#argv + 1] = item.row
          end
          pieces = pieces + 1
        end
        local ok, err = flush(ctx, plan, key, argv)
        if err then return nil, err end
      end
      return true, nil
    end
    local ok, err = batch('del', topo.del_order)
    if err then return nil, err end
    return batch('add', topo.add_order)
  end
end
