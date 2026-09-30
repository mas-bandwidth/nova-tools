-- tset/1 Layer 2, the log: design/L2-CONTRACT.md revision 1, with the decided
-- revision-2 alignments of design/L1-CONTRACT.md section 10 (revision 4) and
-- the decisions of design/L2-COLD-READ-AND-MODEL-2026-09-29.md. The model is
-- tla/SetTableLog.tla: each of its reversed witnesses names a rule this file
-- keeps (counter, refusedlines: seqs come from XINFO in plan and nothing is
-- written before prepare; noopids: lines from changed_ids only; noscore:
-- [before, after] on every member line; split: one prepare list; firstonly,
-- nodedupe: every aligned about, once a line; cursorlimit: next follows the
-- last line returned; perline: one ctx.now_ms; nocap: 1 MiB and 2,000 ids;
-- noceiling: OVERFLOW past 2^53-1; trim: no XTRIM, XDEL, DEL, UNLINK or
-- EXPIRE; shape: the XINFO equality, not the <n>-0 shape).
--
-- L.plan(ctx, table_plan) turns the frozen table plan and ctx.notes into one
-- stream line per emitting entry and per note, allocates every sequence in
-- plan, and returns write descriptors only. L.read(ctx, query) serves last,
-- lines and cardlines. L.read_line_at(ctx, seq, query_index) reads one exact
-- line. Every read goes through S.readcmd, every count through S.charge, and
-- every write is an S.writecmd descriptor; nothing here executes a mutation.
-- S.* helpers are bound at call time: this fragment loads before the
-- validation fragment defines S.json, S.array and the decimal helpers.
if NS.tset_profile then
  local S = NS.tset
  local L = {}
  NS.tlog = L

  local CEILING = '9007199254740991'
  -- A line is the XADD field names, the n value and the body d.
  local MAX_LINE = 1048576
  local MAX_LINE_IDS = 2000
  -- A fetched entry's raw payload is its stream ID plus the capped line.
  -- Eight of these exceed the 8 MiB fetched budget: the worst-case batch is 7.
  local LINE_RAW = MAX_LINE + 64
  -- XINFO STREAM returns the first and last entries beside bounded metadata.
  local XINFO_RESERVE = 2 * LINE_RAW + 4096
  local ITEM_CAP = 524288
  local MAX_REPLY = 8388608
  -- Reserved for this answer's fixed fields and, in page mode, the fields
  -- S.read adds (active_epoch, time_ms, counters).
  local ENVELOPE = 4096
  -- The compact body's container depth: a request's meta is admitted at
  -- depth 16 at most, and the body holds it at depth 2 (the AL5 exact-line
  -- capability's bounded copy).
  local MAX_BODY_DEPTH = 16
  local TAGS = {create = 'c', move = 'm', remove = 'x'}
  local WORDS = {c = 'create', m = 'move', x = 'remove', w = 'rows', a = 'advance', n = 'note'}

  -- Canonical body encoding. Top-level keys are written in the contract's
  -- fixed order by the builders below. Inner object keys are byte-sorted
  -- here, independent of the server's collation locale. Strings use cjson's
  -- escaping (it writes '/' as '\/'). Arrays keep their order.
  local function less(a, b)
    local n = #a < #b and #a or #b
    for i = 1, n do
      local x, y = string.byte(a, i), string.byte(b, i)
      if x ~= y then return x < y end
    end
    return #a < #b
  end

  local function str(s) return S.json.encode(s) end

  local function sorted_keys(map)
    local keys = {}
    for k in pairs(map) do
      if type(k) ~= 'string' then error('tset log: non-string object key', 0) end
      keys[#keys + 1] = k
    end
    table.sort(keys, less)
    return keys
  end

  -- Generic canonical encoder for caller meta. Meta is the one place a JSON
  -- number may appear (the meta exemption); it uses the codec's number form.
  local function encode(v)
    local t = type(v)
    if t == 'string' then return str(v) end
    if v == cjson.null then return 'null' end
    if t == 'boolean' then return v and 'true' or 'false' end
    if t == 'number' then return S.json.encode(v) end
    if t ~= 'table' then error('tset log: unencodable value', 0) end
    local out = {}
    if S.is_array(v) then
      for i = 1, #v do out[i] = encode(v[i]) end
      return '[' .. table.concat(out, ',') .. ']'
    end
    for _, k in ipairs(sorted_keys(v)) do out[#out + 1] = str(k) .. ':' .. encode(v[k]) end
    return '{' .. table.concat(out, ',') .. '}'
  end

  local function strings(list)
    local out = {}
    for i = 1, #list do out[i] = str(list[i]) end
    return '[' .. table.concat(out, ',') .. ']'
  end

  local function object(map)
    local out = {}
    for _, k in ipairs(sorted_keys(map)) do out[#out + 1] = str(k) .. ':' .. str(map[k]) end
    return '{' .. table.concat(out, ',') .. '}'
  end

  local function nullable(v)
    if v == nil or v == cjson.null then return 'null' end
    return str(v)
  end

  local function nonempty(map)
    return type(map) == 'table' and next(map) ~= nil
  end

  local function dedupe(list)
    local seen, out = {}, {}
    for _, v in ipairs(list or {}) do
      if not seen[v] then seen[v] = true; out[#out + 1] = v end
    end
    return out
  end

  local function too_large(size, detail)
    detail.budget = 'log_line_bytes'; detail.actual = size; detail.limit = MAX_LINE
    return S.refuse('LIMIT', detail)
  end

  -- One member line: create, move or remove, from the normalized entry only.
  local function member_line(ctx, e, index)
    local ids, about = e.changed_ids, e.about
    local k = #ids
    local detail = {entry_index = index, table = e.table}
    if k > MAX_LINE_IDS then
      detail.budget = 'log_line_ids'; detail.actual = k; detail.limit = MAX_LINE_IDS
      return nil, S.refuse('LIMIT', detail)
    end
    if type(about) ~= 'table' or #about ~= k then return nil, S.refuse('REQUEST', detail) end
    local parts = {'"k":' .. str(TAGS[e.kind]), '"ms":' .. str(ctx.now_ms), '"tbl":' .. str(e.table)}
    if e.from ~= nil then parts[#parts + 1] = '"from":' .. str(e.from) end
    -- A stay (a move whose destination is its source) omits to.
    if e.to ~= nil and e.to ~= e.from then parts[#parts + 1] = '"to":' .. str(e.to) end
    parts[#parts + 1] = '"ids":' .. strings(ids)
    parts[#parts + 1] = '"about":' .. strings(about)
    local score, rev = {}, {}
    for j = 1, k do
      score[j] = '[' .. nullable(e.before_scores[j]) .. ',' .. nullable(e.after_scores[j]) .. ']'
      rev[j] = '[' .. nullable(e.before_revs[j]) .. ',' .. nullable(e.after_revs[j]) .. ']'
    end
    parts[#parts + 1] = '"score":[' .. table.concat(score, ',') .. ']'
    parts[#parts + 1] = '"rev":[' .. table.concat(rev, ',') .. ']'
    -- A field pair identical across every id is stored once in shared; each
    -- id's set keeps only what differs. Unset names stay per id.
    local sets, unsets, any_unset = {}, {}, false
    for j = 1, k do
      local change = e.field_changes[j] or {}
      sets[j] = change.set or {}
      local names = {}
      for i, name in ipairs(change.unset or {}) do names[i] = name end
      table.sort(names, less)
      unsets[j] = names
      if #names > 0 then any_unset = true end
    end
    local shared = {}
    if k > 0 then
      for f, v in pairs(sets[1]) do
        local same = true
        for j = 2, k do
          if sets[j][f] ~= v then same = false; break end
        end
        if same then shared[f] = v end
      end
    end
    local rest, any_set = {}, false
    for j = 1, k do
      local own = {}
      for f, v in pairs(sets[j]) do
        if shared[f] == nil then own[f] = v; any_set = true end
      end
      rest[j] = own
    end
    if nonempty(shared) then parts[#parts + 1] = '"shared":' .. object(shared) end
    if any_set then
      local out = {}
      for j = 1, k do out[j] = object(rest[j]) end
      parts[#parts + 1] = '"set":[' .. table.concat(out, ',') .. ']'
    end
    if any_unset then
      local out = {}
      for j = 1, k do out[j] = strings(unsets[j]) end
      parts[#parts + 1] = '"unset":[' .. table.concat(out, ',') .. ']'
    end
    if nonempty(e.meta) then parts[#parts + 1] = '"meta":' .. encode(e.meta) end
    return {n = string.format('%d', k), d = '{' .. table.concat(parts, ',') .. '}',
      about = dedupe(about), detail = detail}, nil
  end

  local function rows_line(ctx, e, index)
    local parts = {'"k":"w"', '"ms":' .. str(ctx.now_ms), '"tbl":' .. str(e.table)}
    if #e.added > 0 then
      local out = {}
      for i, item in ipairs(e.added) do
        out[i] = '{"rank":' .. str(item.rank) .. ',"row":' .. str(item.row) .. '}'
      end
      parts[#parts + 1] = '"add":[' .. table.concat(out, ',') .. ']'
    end
    if #e.deleted > 0 then parts[#parts + 1] = '"del":' .. strings(e.deleted) end
    return {n = '0', d = '{' .. table.concat(parts, ',') .. '}',
      detail = {entry_index = index, table = e.table}}
  end

  local function advance_line(ctx, e, index)
    return {n = '0', d = '{"k":"a","ms":' .. str(ctx.now_ms) .. ',"from":' .. str(e.from) ..
      ',"to":' .. str(e.to) .. '}', detail = {entry_index = index}}
  end

  local function note_line(ctx, note, position)
    if type(note) ~= 'table' or type(note.line) ~= 'table' or note.line.kind ~= 'note' or
        type(note.about) ~= 'table' then
      return nil, S.refuse('REQUEST')
    end
    local about = dedupe(note.about)
    if #about > MAX_LINE_IDS then
      return nil, S.refuse('LIMIT', {budget = 'log_line_ids', actual = #about, limit = MAX_LINE_IDS})
    end
    local parts = {'"k":"n"', '"ms":' .. str(ctx.now_ms)}
    if #about > 0 then parts[#parts + 1] = '"about":' .. strings(about) end
    if nonempty(note.line.meta) then parts[#parts + 1] = '"meta":' .. encode(note.line.meta) end
    return {n = string.format('%d', #about), d = '{' .. table.concat(parts, ',') .. '}',
      about = about, note = position, detail = {}}, nil
  end

  local function type_of(ctx, key)
    local value, err = S.readcmd(ctx, {argv = {'TYPE', key}, access = {}}, 16, 'log')
    if err then return nil, err end
    return type(value) == 'table' and value.ok or value, nil
  end

  -- The one XINFO equality: last-generated-id is exactly <entries-added>-0,
  -- with entries-added a Lua number in 0..2^53-1, formatted exactly. A read
  -- also requires length == entries-added, so a deleted entry is seen.
  local function checked_head(ctx, key, gap)
    local info, err = S.readcmd(ctx, {argv = {'XINFO', 'STREAM', key},
      access = {{key = key, kind = 'stream', mode = 'read'}}}, XINFO_RESERVE, 'log')
    if err then return nil, err end
    local h = {}
    for i = 1, #info, 2 do h[info[i]] = info[i + 1] end
    local added = h['entries-added']
    if type(added) ~= 'number' or added ~= math.floor(added) or added < 0 or
        added > 9007199254740991 then
      return nil, S.refuse('LOGID', {budget = 'entries_added'})
    end
    local seq = string.format('%.0f', added)
    if h['last-generated-id'] ~= seq .. '-0' then
      return nil, S.refuse('LOGID', {budget = 'last_generated_id'})
    end
    if gap and h.length ~= added then return nil, S.refuse('LOGID', {budget = 'length'}) end
    return seq, nil
  end

  -- The head of the log this step appends to. An absent key is a fresh epoch
  -- (next seq 1), unless a touched history key already exists: then the log
  -- was deleted under its histories, DRIFT. An advance's new-epoch log key
  -- must be absent.
  local function write_head(ctx, key, abouts)
    local advancing = ctx.write_epoch ~= ctx.request_epoch
    local kind, err = type_of(ctx, key)
    if err then return nil, err end
    if kind == 'none' then
      for _, about in ipairs(abouts) do
        local other
        other, err = type_of(ctx, ctx.history_key(ctx.write_epoch, about))
        if err then return nil, err end
        if other == 'list' or (other ~= 'none' and advancing) then
          return nil, S.refuse('DRIFT', {ids = {about}})
        end
        if other ~= 'none' then return nil, S.refuse('WRONGTYPE', {ids = {about}}) end
      end
      return '0', nil
    end
    if advancing then return nil, S.refuse('DRIFT') end
    if kind ~= 'stream' then return nil, S.refuse('WRONGTYPE') end
    return checked_head(ctx, key, false)
  end

  function L.plan(ctx, table_plan)
    if type(ctx) ~= 'table' or ctx.operation ~= 'step' or type(table_plan) ~= 'table' or
        type(table_plan.entries) ~= 'table' then
      return nil, S.refuse('REQUEST')
    end
    -- A missing TIME sample is an unexpected runtime error, not a refusal.
    if type(ctx.now_ms) ~= 'string' then error('tset log: TIME was not sampled for this call', 0) end
    local notes = ctx.notes
    if type(notes) ~= 'table' or notes ~= ctx.request.notes then return nil, S.refuse('REQUEST') end
    local originals = ctx.request.entries or {}
    local lines = {}
    -- Member and topology lines in combined entry order. A guard, a count,
    -- a guard-only rowset, an unchanged rows entry and an entry whose
    -- effective changed set is empty emit nothing.
    for ix = 1, #table_plan.entries do
      local e = table_plan.entries[ix]
      local kind = type(e) == 'table' and e.kind or nil
      local line, err
      if kind == 'create' or kind == 'move' or kind == 'remove' then
        -- The composed profile requires about on every member-changing entry.
        if type(originals[ix]) ~= 'table' or originals[ix].about == nil then
          return nil, S.refuse('REQUEST', {entry_index = ix - 1, table = e.table})
        end
        if #e.changed_ids > 0 then
          line, err = member_line(ctx, e, ix - 1)
          if err then return nil, err end
        end
      elseif kind == 'rows' then
        if #e.added > 0 or #e.deleted > 0 then line = rows_line(ctx, e, ix - 1) end
      elseif kind == 'advance' then
        line = advance_line(ctx, e, ix - 1)
      end
      if line then lines[#lines + 1] = line end
    end
    for i = 1, #notes do
      local line, err = note_line(ctx, notes[i], i)
      if err then return nil, err end
      lines[#lines + 1] = line
    end
    -- Every size is counted before the first read of the log and before any
    -- write. A line is never split, and no id or field is dropped.
    local generated, order, seen = 0, {}, {}
    for _, line in ipairs(lines) do
      local size = 2 + #line.n + #line.d
      if size > MAX_LINE then return nil, too_large(size, line.detail) end
      generated = generated + size
      for _, about in ipairs(line.about or {}) do
        if not seen[about] then seen[about] = true; order[#order + 1] = about end
      end
    end
    local note_seqs = S.array()
    local count = #lines
    if count == 0 then
      return {commands = {}, first_seq = '0', last_seq = '0', line_count = 0,
        about_appends = 0, note_seqs = note_seqs}, nil
    end
    local key = ctx.log_key(ctx.write_epoch)
    local head, err = write_head(ctx, key, order)
    if err then return nil, err end
    local seqs, seq = {}, head
    for i = 1, count do
      seq = S.next(seq)
      if not seq or S.cmp(seq, CEILING) > 0 then
        return nil, S.refuse('OVERFLOW', {budget = 'log_seq'})
      end
      seqs[i] = seq
    end
    local commands, by_about, appends = {}, {}, 0
    for i, line in ipairs(lines) do
      local d
      d, err = S.writecmd(ctx, {'XADD', key, seqs[i] .. '-0', 'n', line.n, 'd', line.d},
        {{key = key, kind = 'stream', mode = 'write'}})
      if err then return nil, err end
      commands[#commands + 1] = d
      for _, about in ipairs(line.about or {}) do
        local list = by_about[about]
        if not list then list = {}; by_about[about] = list end
        list[#list + 1] = seqs[i]
        appends = appends + 1
      end
      if line.note then note_seqs[line.note] = seqs[i] end
    end
    -- One RPUSH per history key per step, its seqs ascending.
    for _, about in ipairs(order) do
      local hkey = ctx.history_key(ctx.write_epoch, about)
      local argv = {'RPUSH', hkey}
      for _, s in ipairs(by_about[about]) do argv[#argv + 1] = s end
      local d
      d, err = S.writecmd(ctx, argv, {{key = hkey, kind = 'list', mode = 'write'}})
      if err then return nil, err end
      commands[#commands + 1] = d
    end
    -- Layer 1 has no checked helper for this reporting counter.
    ctx.budget.generated_log_bytes = (ctx.budget.generated_log_bytes or 0) + generated
    return {commands = commands, first_seq = seqs[1], last_seq = seqs[count],
      line_count = count, about_appends = appends, note_seqs = note_seqs}, nil
  end

  ------------------------------------------------------------------ reads

  -- Layer 1's exact-line capability (AL5): authorize_line checks the sealed
  -- read context and query index and names the sealed log key;
  -- read_line_raw fetches one exact line under this layer's reservation.
  -- Both stay lexical here. The caches of decoded lines and of observed
  -- tails belong to one read context and are cleared by Layer 1 when the
  -- context ends, so nothing a callback writes into ctx can feed them.
  local authorize_line, read_line_raw
  local cache_ctx, cached_lines, cached_tails
  S.bind_log_helpers(LINE_RAW, function(authorize, raw)
    authorize_line, read_line_raw = authorize, raw
    return function() cache_ctx, cached_lines, cached_tails = nil, nil, nil end
  end)

  local function context_cache(ctx)
    if cache_ctx ~= ctx then cache_ctx, cached_lines, cached_tails = ctx, {}, {} end
  end

  -- The container depth of a stored body, before it is decoded: strings and
  -- their escapes are skipped whole.
  local function body_depth_ok(d)
    local depth, i, n = 0, 1, #d
    while i <= n do
      local at = string.find(d, '[%[%]{}"]', i)
      if not at then return true end
      local c = string.sub(d, at, at)
      if c == '"' then
        local j = at + 1
        while true do
          local q = string.find(d, '["\\]', j)
          if not q then return false end
          if string.sub(d, q, q) == '"' then i = q + 1; break end
          j = q + 2
        end
      else
        if c == '[' or c == '{' then
          depth = depth + 1
          if depth > MAX_BODY_DEPTH then return false end
        else
          depth = depth - 1
        end
        i = at + 1
      end
    end
    return true
  end

  -- A bounded deep copy of a decoded body: arrays stay arrays, cjson.null
  -- stays null, and the copy visits at most budget nodes, keys and bytes.
  local function copy_body(v, depth, budget)
    local t = type(v)
    if t ~= 'table' then
      if t == 'string' then budget.left = budget.left - #v end
      budget.left = budget.left - 1
      if budget.left < 0 then return nil, false end
      return v, true
    end
    if v == cjson.null then return v, true end
    if depth > MAX_BODY_DEPTH then return nil, false end
    budget.left = budget.left - 1
    local out = S.is_array(v) and S.array() or {}
    for k, item in pairs(v) do
      if type(k) == 'string' then budget.left = budget.left - #k end
      local copied, ok = copy_body(item, depth + 1, budget)
      if not ok then return nil, false end
      out[k] = copied
    end
    return out, true
  end

  local function fail(code, index, extra)
    local detail = {query_index = index}
    for k, v in pairs(extra or {}) do detail[k] = v end
    return S.refuse(code, detail)
  end

  local function exhausted_code(ctx)
    return ctx.operation == 'read' and 'BUDGET' or 'LIMIT'
  end

  -- The room this answer may use in the encoded reply: the shared 8 MiB less
  -- what earlier answers used, less a reserved envelope. bytes_limit is not
  -- admitted by Layer 1's static validator; it is honoured here if it
  -- arrives.
  local function answer_room(ctx, q)
    local cap = MAX_REPLY - (ctx.read_encoded or 512) - 1
    if type(q.bytes_limit) == 'number' and q.bytes_limit < cap then cap = q.bytes_limit end
    return cap - ENVELOPE
  end

  local function read_tail(ctx, key)
    context_cache(ctx)
    local tails = cached_tails
    if tails[key] then return tails[key], nil end
    local kind, err = type_of(ctx, key)
    if err then return nil, err end
    local tail
    if kind == 'none' then tail = '0'
    elseif kind ~= 'stream' then return nil, S.refuse('WRONGTYPE')
    else
      tail, err = checked_head(ctx, key, true)
      if err then return nil, err end
    end
    tails[key] = tail
    return tail, nil
  end

  -- An entry must be <expect>-0 with exactly the fields n and d, in that order.
  local function parse_entry(entry, expect, index)
    if type(entry) ~= 'table' or type(entry[1]) ~= 'string' then
      return nil, nil, fail('DRIFT', index, {budget = 'log_entry'})
    end
    if entry[1] ~= expect .. '-0' then return nil, nil, fail('LOGID', index, {budget = 'log_gap'}) end
    local f = entry[2]
    if type(f) ~= 'table' or #f ~= 4 or f[1] ~= 'n' or f[3] ~= 'd' or type(f[2]) ~= 'string' or
        type(f[4]) ~= 'string' or not S.uint(f[2]) or #f[2] > 4 or tonumber(f[2]) > MAX_LINE_IDS or
        2 + #f[2] + #f[4] > MAX_LINE then
      return nil, nil, fail('DRIFT', index, {budget = 'log_entry'})
    end
    return f[2], f[4], nil
  end

  -- Fetch and decode one exact line once per call through the exact-line
  -- capability; later uses are cache hits with no fetch and no second
  -- raw-byte charge, and each is authorized again first. short is true when
  -- the raw budget cannot reserve a maximum line.
  local function fetch_line(ctx, seq, index)
    local _, aerr = authorize_line(ctx, seq, index)
    if aerr then return nil, aerr, false end
    context_cache(ctx)
    local hit = cached_lines[seq]
    if hit then return hit, nil, false end
    local batch, err, short = read_line_raw(ctx, seq, index)
    if err then return nil, err, false end
    if short then return nil, nil, true end
    -- A history seq with no line: the log is behind its history.
    if type(batch) ~= 'table' or #batch ~= 1 then return nil, fail('LOGID', index, {budget = 'log_line'}) end
    local n, d, perr = parse_entry(batch[1], seq, index)
    if perr then return nil, perr end
    if not body_depth_ok(d) then return nil, fail('DRIFT', index, {budget = 'log_body'}) end
    local ok, body = pcall(S.json.decode, d)
    if not ok or type(body) ~= 'table' or S.is_array(body) or not WORDS[body.k] or
        type(body.ms) ~= 'string' then
      return nil, fail('DRIFT', index, {budget = 'log_body'})
    end
    local count = 0
    if body.k == 'c' or body.k == 'm' or body.k == 'x' then
      count = type(body.ids) == 'table' and #body.ids or -1
    elseif body.k == 'n' then
      count = type(body.about) == 'table' and #body.about or 0
    end
    -- n disagreeing with the body is DRIFT.
    if count ~= tonumber(n) then return nil, fail('DRIFT', index, {budget = 'log_id_count'}) end
    local line = {seq = seq, n = n, d = d, body = body}
    cached_lines[seq] = line
    return line, nil, false
  end

  local function last_query(ctx, q, index)
    local tail, err = read_tail(ctx, ctx.log_key(ctx.request_epoch))
    if err then return nil, err end
    return {kind = 'last', last_seq = tail}, nil
  end

  local function lines_query(ctx, q, index, page)
    local key = ctx.log_key(ctx.request_epoch)
    local tail, err = read_tail(ctx, key)
    if err then return nil, err end
    local high = tail
    -- Open for revision 2 of the Layer 2 contract: the high-water when an
    -- explicit through_seq is given. Revision 1's text (L1 section 7 as adopted) captures the tail
    -- when through_seq is absent and emits only after_seq < seq <= through_seq
    -- when it is given; a lines request carries no other cursor, so a given
    -- through_seq is the fixed high-water, and one above the tail is CURSOR.
    if q.through_seq ~= nil then
      if S.cmp(q.through_seq, tail) > 0 then return nil, fail('CURSOR', index, {budget = 'through_seq'}) end
      high = q.through_seq
    end
    if S.cmp(q.after_seq, high) > 0 then return nil, fail('CURSOR', index, {budget = 'after_seq'}) end
    local ids_cap = q.ids_limit or S.limits.log_id
    local room = answer_room(ctx, q)
    local items, cur, ids, bytes = S.array(), q.after_seq, 0, 0
    -- Both are at most the live ceiling here, so the difference is exact.
    local remaining = tonumber(high) - tonumber(cur)
    local stop
    while not stop and #items < q.limit and remaining > 0 do
      local want = math.min(q.limit - #items, remaining)
      local fits = math.floor((S.limits.fetched_bytes - ctx.budget.fetched_bytes) / LINE_RAW)
      local k = math.min(want, fits)
      if k < 1 then stop = 'fetched_bytes'; break end
      local first = S.next(cur)
      local last = first
      for _ = 2, k do last = S.next(last) end
      -- The batch is issued only when its worst case fits the raw budget.
      local batch
      batch, err = S.readcmd(ctx, {argv = {'XRANGE', key, first .. '-0', last .. '-0', 'COUNT',
        string.format('%d', k)}, access = {{key = key, kind = 'stream', mode = 'read'}}},
        k * LINE_RAW, 'log')
      if err then return nil, err end
      if type(batch) ~= 'table' or #batch > k then return nil, fail('DRIFT', index, {budget = 'log_entry'}) end
      local expect = first
      for i = 1, k do
        -- Every seq up to the high-water exists: a missing one is a hole.
        if batch[i] == nil then return nil, fail('LOGID', index, {budget = 'log_gap'}) end
        local n, d, perr = parse_entry(batch[i], expect, index)
        if perr then return nil, perr end
        local count = tonumber(n)
        local id_room = math.min(ids_cap - ids, S.limits.log_id - (ctx.budget.log_id or 0))
        if count > id_room then stop = 'log_id'; break end
        local item = {seq = expect, n = n, d = d}
        local size = #S.json.encode(item) + 1
        if bytes + size > room then stop = 'encoded_reply'; break end
        local ok
        ok, err = S.charge(ctx, 'log_id', count)
        if err then return nil, err end
        items[#items + 1] = item
        bytes, ids, cur = bytes + size, ids + count, expect
        remaining = remaining - 1
        expect = S.next(expect)
      end
    end
    -- A line is never cut: BUDGET only when not even one line fits.
    if #items == 0 and remaining > 0 then
      return nil, fail(exhausted_code(ctx), index, {budget = stop or 'fetched_bytes'})
    end
    if page then
      return {status = 'page', epoch = ctx.request_epoch, items = items, next = S.next(cur),
        through = high, exhausted = cur == high}, nil
    end
    -- Open for revision 2 of the Layer 2 contract: the empty atomic answer.
    -- next is the first seq not returned, as written; through is "0" when no
    -- line is returned, revision 1's own spelling for "no line" (log_plan's
    -- seqs).
    return {kind = 'lines', lines = items, next = S.next(cur),
      through = #items > 0 and cur or '0'}, nil
  end

  -- The projection of one stored line onto one primary: one item per member
  -- id whose about is that primary, or the note itself. Other primaries' ids,
  -- fields and the full arrays are never copied out.
  local function project(line, about, fields, include_meta, index)
    local b, out = line.body, {}
    if b.k == 'n' then
      local named = false
      for _, p in ipairs(type(b.about) == 'table' and b.about or {}) do
        if p == about then named = true; break end
      end
      if not named then return nil, fail('DRIFT', index, {budget = 'history', ids = {about}}) end
      local item = {seq = line.seq, kind = 'note', at_ms = b.ms}
      if include_meta and b.meta ~= nil then item.meta = copy_body(b.meta, 2, {left = 2 * #line.d + 64}) end
      out[1] = item
      return out, nil
    end
    local ids, abouts = b.ids, b.about
    if (b.k ~= 'c' and b.k ~= 'm' and b.k ~= 'x') or type(abouts) ~= 'table' or #abouts ~= #ids or
        type(b.score) ~= 'table' or #b.score ~= #ids or type(b.rev) ~= 'table' or #b.rev ~= #ids or
        (b.set ~= nil and (type(b.set) ~= 'table' or #b.set ~= #ids)) or
        (b.unset ~= nil and (type(b.unset) ~= 'table' or #b.unset ~= #ids)) then
      return nil, fail('DRIFT', index, {budget = 'history', ids = {about}})
    end
    if b.shared ~= nil and not S.is_object(b.shared) then
      return nil, fail('DRIFT', index, {budget = 'log_body'})
    end
    local shared = b.shared or {}
    for j = 1, #ids do
      -- A stored element of the wrong type is a broken stored invariant.
      if type(ids[j]) ~= 'string' or type(abouts[j]) ~= 'string' or
          (b.set ~= nil and type(b.set[j]) ~= 'table') or
          (b.unset ~= nil and type(b.unset[j]) ~= 'table') then
        return nil, fail('DRIFT', index, {budget = 'log_body'})
      end
      if abouts[j] == about then
        local item = {seq = line.seq, kind = WORDS[b.k], at_ms = b.ms, table = b.tbl,
          id = ids[j], score = b.score[j], rev = b.rev[j]}
        if b.from ~= nil then item.from = b.from end
        if b.to ~= nil then item.to = b.to end
        local own = b.set and b.set[j] or {}
        local gone = {}
        for _, name in ipairs(b.unset and b.unset[j] or {}) do
          if type(name) ~= 'string' then return nil, fail('DRIFT', index, {budget = 'log_body'}) end
          gone[name] = true
        end
        local set, unset = {}, S.array()
        for _, f in ipairs(fields) do
          local v = own[f]
          if v == nil then v = shared[f] end
          if v ~= nil and type(v) ~= 'string' then return nil, fail('DRIFT', index, {budget = 'log_body'}) end
          if v ~= nil and set[f] == nil then set[f] = v
          elseif gone[f] then gone[f] = nil; unset[#unset + 1] = f end
        end
        if next(set) ~= nil then item.set = set end
        if #unset > 0 then item.unset = unset end
        if include_meta and b.meta ~= nil then item.meta = copy_body(b.meta, 2, {left = 2 * #line.d + 64}) end
        out[#out + 1] = item
      end
    end
    if #out == 0 then return nil, fail('DRIFT', index, {budget = 'history', ids = {about}}) end
    return out, nil
  end

  local function cardlines_query(ctx, q, index, page)
    local abouts = q.abouts
    local seen = {}
    for _, p in ipairs(abouts) do
      if seen[p] then return nil, fail('REQUEST', index, {ids = {p}}) end
      seen[p] = true
    end
    local fields = S.array()
    for i, f in ipairs(q.fields or {}) do fields[i] = f end
    local include_meta = q.include_meta == true
    local cursor = q.cursor
    -- Cursor identity: epoch, projection, metadata switch, abouts and order.
    if cursor ~= nil then
      local same = cursor.epoch == ctx.request_epoch and cursor.include_meta == include_meta and
        #cursor.fields == #fields and #cursor.positions == #abouts
      for i = 1, same and #fields or 0 do
        if cursor.fields[i] ~= fields[i] then same = false end
      end
      for i = 1, same and #abouts or 0 do
        if cursor.positions[i].about ~= abouts[i] then same = false end
      end
      if not same then return nil, fail('CURSOR', index) end
    end
    local positions, slots = {}, S.array()
    for i, about in ipairs(abouts) do
      local key = ctx.history_key(ctx.request_epoch, about)
      local llen, err = S.readcmd(ctx, {argv = {'LLEN', key},
        access = {{key = key, kind = 'list', mode = 'read'}}}, 32, 'log')
      if err then return nil, err end
      if type(llen) ~= 'number' or llen < 0 or llen > 9007199254740991 then
        return nil, fail('DRIFT', index, {budget = 'history', ids = {about}})
      end
      local pos = {about = about, key = key, next_index = 0, through_index = llen - 1, next_item = 0}
      if cursor ~= nil then
        local c = cursor.positions[i]
        -- The item half of the pair is next_item, zero-based, present only
        -- when a page ended inside a line; its name is open for revision 2 of
        -- the Layer 2 contract (L1 10, item 4).
        local item = c.next_item == nil and 0 or c.next_item
        if c.through_index > llen - 1 or c.next_index > c.through_index + 1 or
            type(item) ~= 'number' or item < 0 or item ~= math.floor(item) or
            (item > 0 and c.next_index > c.through_index) then
          return nil, fail('CURSOR', index, {ids = {about}})
        end
        pos.next_index, pos.through_index, pos.next_item = c.next_index, c.through_index, item
      end
      positions[i] = pos
      slots[i] = {about = about, lines = S.array()}
    end
    -- The fixed parts of the answer: slots, the cursor and the high-waters.
    local fixed = 2 * #S.json.encode(fields) + 128
    for _, about in ipairs(abouts) do fixed = fixed + 2 * #str(about) + 160 end
    local room = answer_room(ctx, q) - fixed
    local limit, total, bytes = q.limit, 0, 0
    local stop, stop_detail
    for i, pos in ipairs(positions) do
      if stop then break end
      while not stop and pos.next_index <= pos.through_index do
        if total >= limit then stop = 'limit'; break end
        local count = math.min(pos.through_index - pos.next_index + 1, limit - total)
        local seqs, err = S.readcmd(ctx, {argv = {'LRANGE', pos.key, string.format('%d', pos.next_index),
          string.format('%d', pos.next_index + count - 1)},
          access = {{key = pos.key, kind = 'list', mode = 'read'}}}, count * 24, 'log')
        if err then return nil, err end
        -- Appends past the high-water never enter; a list shorter than its
        -- captured high-water was changed outside the supported writer.
        if type(seqs) ~= 'table' or #seqs ~= count then
          return nil, fail('DRIFT', index, {budget = 'history', ids = {pos.about}})
        end
        for _, seq in ipairs(seqs) do
          -- A history is strictly ascending: a repeated or earlier seq is a
          -- list changed outside the supported writer.
          if not S.uint(seq) or seq == '0' or S.cmp(seq, CEILING) > 0 or
              (pos.last_seq and S.cmp(seq, pos.last_seq) <= 0) then
            return nil, fail('DRIFT', index, {budget = 'history', ids = {pos.about}})
          end
          pos.last_seq = seq
          local line, lerr, short = fetch_line(ctx, seq, index)
          if lerr then return nil, lerr end
          if short then stop = 'fetched_bytes'; break end
          local projected
          projected, lerr = project(line, pos.about, fields, include_meta, index)
          if lerr then return nil, lerr end
          if pos.next_item >= #projected then return nil, fail('CURSOR', index, {ids = {pos.about}}) end
          for j = pos.next_item + 1, #projected do
            if total >= limit then stop = 'limit'; break end
            local item = projected[j]
            local size = #S.json.encode(item)
            if size > ITEM_CAP then
              stop = 'cardlines_item'; stop_detail = {actual = size, limit = ITEM_CAP}; break
            end
            if bytes + size + 1 > room then stop = 'encoded_reply'; break end
            local field_charge = item.id and #fields or 0
            if S.limits.log_id - (ctx.budget.log_id or 0) < 1 then stop = 'log_id'; break end
            if S.limits.read_fields - (ctx.budget.field or 0) < field_charge then stop = 'field'; break end
            local ok
            ok, err = S.charge(ctx, 'log_id', 1)
            if err then return nil, err end
            if field_charge > 0 then
              ok, err = S.charge(ctx, 'field', field_charge)
              if err then return nil, err end
            end
            slots[i].lines[#slots[i].lines + 1] = item
            total, bytes, pos.next_item = total + 1, bytes + size + 1, j
          end
          if stop then break end
          pos.next_index, pos.next_item = pos.next_index + 1, 0
        end
      end
    end
    local exhausted = true
    for _, pos in ipairs(positions) do
      if pos.next_index <= pos.through_index then exhausted = false end
    end
    local function budget_refusal()
      local extra = {budget = stop or 'limit'}
      for k, v in pairs(stop_detail or {}) do extra[k] = v end
      return fail('BUDGET', index, extra)
    end
    if not page then
      -- An atomic cardlines answer is the whole selection or a refusal.
      if not exhausted then return nil, budget_refusal() end
      return {kind = 'cardlines', lines = slots, next = cjson.null, through = cjson.null}, nil
    end
    -- A nonempty remaining page emits at least one item or refuses.
    if total == 0 and not exhausted then return nil, budget_refusal() end
    local through, cursor_positions = S.array(), S.array()
    for i, pos in ipairs(positions) do
      through[i] = pos.through_index
      local p = {about = pos.about, next_index = pos.next_index, through_index = pos.through_index}
      if pos.next_item > 0 then p.next_item = pos.next_item end
      cursor_positions[i] = p
    end
    local continuation = cjson.null
    if not exhausted then
      continuation = {epoch = ctx.request_epoch, fields = fields, include_meta = include_meta,
        positions = cursor_positions}
    end
    return {status = 'page', epoch = ctx.request_epoch, items = slots, next = continuation,
      through = through, exhausted = exhausted}, nil
  end

  function L.read(ctx, q)
    local index = ctx.query_index
    local page = ctx.request.mode == 'page'
    if q.kind == 'last' then return last_query(ctx, q, index) end
    if q.kind == 'lines' then return lines_query(ctx, q, index, page) end
    if q.kind == 'cardlines' then return cardlines_query(ctx, q, index, page) end
    return nil, fail('REQUEST', index)
  end

  -- One exact line by seq, decoded: ids, about and meta, with the common
  -- fetched-byte, probe and log_id charges. A fetched line is reused within
  -- the call, but each use charges its ids. The ids, about and meta tables
  -- are the call's cached decode: a caller reads them and never changes them.
  function L.read_line_at(ctx, seq, index)
    if type(seq) ~= 'string' or not S.uint(seq) or seq == '0' or S.cmp(seq, CEILING) > 0 then
      return nil, fail('REQUEST', index)
    end
    local line, err, short = fetch_line(ctx, seq, index)
    if err then return nil, err end
    if short then return nil, fail(exhausted_code(ctx), index, {budget = 'fetched_bytes'}) end
    local ok
    ok, err = S.charge(ctx, 'log_id', tonumber(line.n))
    if err then return nil, err end
    -- The caller gets one bounded copy of the cached decode, never the
    -- cache's own tables.
    local b, copied = copy_body(line.body, 1, {left = 2 * #line.d + 64})
    if not copied then return nil, fail('DRIFT', index, {budget = 'log_body'}) end
    return {seq = seq, n = line.n, kind = WORDS[b.k], at_ms = b.ms, table = b.tbl,
      from = b.from, to = b.to, ids = b.ids or S.array(), about = b.about or S.array(),
      meta = b.meta, d = line.d}, nil
  end
end
