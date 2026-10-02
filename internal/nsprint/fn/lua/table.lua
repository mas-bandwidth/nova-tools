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
  -- T.kind(key): the Redis type of key ('none' when absent).
  function T.kind(key)
    local t = redis.call('TYPE', key)
    return (type(t) == 'table' and t.ok) and t.ok or t
  end
  -- T.hash(key): the hash at key as a table, and its flat form. A value of
  -- another type raises {wrongtype=...}, which T.top (every registered
  -- function runs under it) turns into the refusal WRONGTYPE naming the key
  -- and the type found; a wrong-type key never leaves as a raw Redis error.
  function T.hash(key)
    local flat = redis.pcall('HGETALL', key)
    if type(flat) == 'table' and flat.err then
      if string.find(flat.err, 'WRONGTYPE') then error({wrongtype = true, key = key, found = T.kind(key), want = 'hash'}) end
      T.rethrow(flat)
    end
    local h = {}
    for i = 1, #flat, 2 do h[flat[i]] = flat[i + 1] end
    return h, flat
  end
  -- T.rethrow(res): raise again an error a pcall caught, as it came: the store
  -- writes "ERR " in front of a message that names no code, so the copy this
  -- function raises gives it back without the prefix it already carries.
  function T.rethrow(res)
    if type(res) == 'table' and res.err then
      error({err = (string.gsub(res.err, '^ERR ', '', 1))}, 0)
    end
    error(res, 0)
  end
  -- T.top(callback): the wrapper every registered function runs under.
  function T.top(callback)
    return function(keys, args)
      local ok, res = pcall(callback, keys, args)
      if ok then return res end
      if type(res) == 'table' and res.wrongtype then return T.refuse('WRONGTYPE', res.key, res.found, res.want) end
      error(res, 0)
    end
  end
  -- From here on `redis` is a shim whose register_function runs every
  -- function under T.top. (Library load runs before the standard globals
  -- exist, so nothing here calls type().)
  -- (redis.call and its kin do not exist while the library loads, so the shim
  -- forwards to them when a function runs.)
  local redis = {
    -- A command answered WRONGTYPE: the refusal names its key, the type found and
    -- the type the command wants. It never leaves as a raw Redis error.
    call = function(...)
      local res = redis.pcall(...)
      if type(res) == 'table' and res.err then
        if string.find(res.err, 'WRONGTYPE') then
          local command, key = ...
          if command == 'XINFO' then key = select(3, ...) end
          local family = {H = 'hash', Z = 'zset', S = 'set', X = 'stream', L = 'list'}
          error({wrongtype = true, key = key, found = T.kind(key), want = family[string.sub(command, 1, 1)] or 'value'})
        end
        T.rethrow(res)
      end
      return res
    end,
    pcall = function(...) return redis.pcall(...) end,
    sha1hex = function(...) return redis.sha1hex(...) end,
    acl_check_cmd = function(...) return redis.acl_check_cmd(...) end,
    register_function = function(spec, callback)
      if callback == nil then
        spec.callback = T.top(spec.callback)
        return redis.register_function(spec)
      end
      return redis.register_function(spec, T.top(callback))
    end,
  }
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
  -- T.uintgt(a, b): decimal uint64 strings, a > b.
  function T.uintgt(a, b)
    if #a ~= #b then return #a > #b end
    return a > b
  end
  -- The bounds of a batch and of a read set. internal/ntable/limits.go holds
  -- the same numbers and docs/SPEC-NOVA-TABLE.md states them; a test compares
  -- the three. A refusal names the bound and the count found, never the input.
  T.limits = {
    manifest_bytes = 1048576, changed_entries = 128, guard_entries = 1024,
    member_id_bytes = 256, field_value_bytes = 65536,
    set_fields = 128, unset_fields = 1000, field_guards = 1000, one_of_options = 1000,
    read_set_members = 1024,
    columns = 1000, rows = 100000,
    manifest_props = 64, table_props = 64,
    receipt_bytes = 1048576, batch_value_bytes = 16777216,
  }
  T.limit_names = {
    manifest_bytes = 'manifest bytes', changed_entries = 'entries with changes', guard_entries = 'guard-only entries',
    member_id_bytes = 'member id bytes', field_value_bytes = 'field value bytes',
    set_fields = 'set fields per member', unset_fields = 'unset fields per member',
    field_guards = 'guards per member', one_of_options = 'one_of options', read_set_members = 'read set members',
    columns = 'columns per table', rows = 'rows per table',
    manifest_props = 'properties per manifest', table_props = 'properties per table',
    receipt_bytes = 'receipt bytes', batch_value_bytes = 'value bytes per batch',
  }
  -- T.over(key, observed, member): a LIMIT refusal when observed exceeds the bound.
  function T.over(key, observed, member)
    local bound = T.limits[key]
    if observed <= bound then return nil end
    return T.refuse('LIMIT', T.limit_names[key], bound, observed, member)
  end
  -- T.over_least(key, observed, member): the same refusal for a count the call
  -- stopped at when it passed the bound, so the true size is at least this: the
  -- sixth element of the refusal names the member ('' for none), the seventh says so.
  function T.over_least(key, observed, member)
    local bound = T.limits[key]
    if observed <= bound then return nil end
    return T.refuse('LIMIT', T.limit_names[key], bound, observed, member or '', 'at least')
  end
  -- T.excerpt(s): s as a refusal may carry it: a value over 64 bytes is its first
  -- 32 and its length, so a refusal never echoes a long input.
  function T.excerpt(s)
    if type(s) ~= 'string' or #s <= 64 then return s end
    return string.sub(s, 1, 32) .. '...(' .. #s .. ' bytes)'
  end
  -- A receipt, the change event and the operation record's result hold a field value in
  -- full when it is at most this many bytes; a longer one is its length and its
  -- SHA-1 (sha1hex is the digest the script API has). internal/ntable/limits.go
  -- holds the same number (ReceiptValueBytes); a test compares them.
  T.receipt_value_bytes = 64
  -- The longest text of a score read back from the store: a double as the store
  -- prints it ("-1.7976931348623157e+308").
  T.score_text_bytes = 24
  -- T.receipt_size(encode, late): the byte length of the receipt's delta as encode
  -- makes it, known before the first write. Each score a call reads back after its
  -- writes (late[i].delta.after_score) is counted at its longest form, so the size
  -- is never less than the size the receipt ends with; the scores are put back.
  function T.receipt_size(encode, late)
    local held = {}
    for i, l in ipairs(late) do
      held[i] = l.delta.after_score
      l.delta.after_score = string.rep('9', T.score_text_bytes)
    end
    local size = #encode()
    for i, l in ipairs(late) do l.delta.after_score = held[i] end
    return size
  end
  -- T.fieldchange(before, after): the change of one field as a receipt records it.
  -- Absent is null with no bytes; a value too long to record is null with its
  -- bytes and sha1.
  function T.fieldchange(before, after)
    local change = {before = cjson.null, after = cjson.null}
    for _, side in ipairs({{'before', before}, {'after', after}}) do
      local key, value = side[1], side[2]
      if value ~= nil then
        if #value > T.receipt_value_bytes then
          change[key .. '_bytes'] = #value
          change[key .. '_sha1'] = redis.sha1hex(value)
        else
          change[key] = value
        end
      end
    end
    return change
  end
  -- T.smallvalues(set): the fields of a set instruction whose values a receipt
  -- records in full; the others appear in the receipt's fields by length and sha1.
  function T.smallvalues(set)
    local kept = {}
    for f, v in pairs(set or {}) do
      if #v <= T.receipt_value_bytes then kept[f] = v end
    end
    return kept
  end
  function T.name(n) return type(n) == 'string' and string.match(n, '^[%w_][%w_.-]*$') end
  function T.word(n) return type(n) == 'string' and n ~= '' and not string.find(n, '%c') end
  -- T.formula(proj): a formula projection read (internal/ntable ParseFormula):
  -- pct(<col>), pct(<col>/<a>+<b>+...) or sum(<a>+<b>+...). It returns
  -- {sum=bool, inputs={every column named, each once}}, or nil when proj is
  -- not a well-formed formula.
  function T.formula(proj)
    if type(proj) ~= 'string' then return nil end
    local kind, arg = string.match(proj, '^(%a%a%a)%((.*)%)$')
    if kind ~= 'pct' and kind ~= 'sum' then return nil end
    local inputs, seen = {}, {}
    local function add(list)
      for term in string.gmatch(list .. '+', '([^+]*)%+') do
        if not T.name(term) or seen[term] then return false end
        seen[term] = true
        inputs[#inputs + 1] = term
      end
      return true
    end
    if kind == 'sum' then
      if not add(arg) then return nil end
      return {sum=true, inputs=inputs}
    end
    local part, over = string.match(arg, '^([^/]*)/(.*)$')
    if not part then part = arg end
    if not T.name(part) then return nil end
    seen[part] = true
    inputs[1] = part
    if over then
      seen = {}
      if not add(over) then return nil end
      -- the numerator is read once, whether or not the denominator names it
      local once, dup = {}, {}
      for _, name in ipairs(inputs) do if not dup[name] then dup[name] = true; once[#once + 1] = name end end
      inputs = once
    end
    return {sum=false, inputs=inputs}
  end
  -- Redis strings are arbitrary bytes; row identities also travel in JSON.
  -- Reject malformed UTF-8 instead of letting a client rename them to U+FFFD.
  function T.row(n)
    return T.word(n) and T.utf8(n)
  end
  -- T.utf8(n): n is well-formed UTF-8 (no overlong forms, surrogates or
  -- code points past U+10FFFF).
  function T.utf8(n)
    -- all ASCII is valid UTF-8: one scan in C, not one byte at a time in Lua
    if not string.find(n, '[\128-\255]') then return true end
    local i = 1
    local function continuation(v) return v and v >= 128 and v <= 191 end
    while i <= #n do
      local a, b, c, e = string.byte(n, i, i + 3)
      if a < 128 then i = i + 1
      elseif a >= 194 and a <= 223 and continuation(b) then i = i + 2
      elseif a >= 224 and a <= 239 and continuation(b) and continuation(c) and
          (a ~= 224 or b >= 160) and (a ~= 237 or b <= 159) then i = i + 3
      elseif a >= 240 and a <= 244 and continuation(b) and continuation(c) and continuation(e) and
          (a ~= 240 or b >= 144) and (a ~= 244 or b <= 143) then i = i + 4
      else return false end
    end
    return true
  end
  -- cjson maps both [] and {} to an empty Lua table on supported Redis
  -- versions. Inspect the already-decoded JSON's top-level field token so
  -- an object cannot masquerade as an empty replacement row list. Skip
  -- quoted strings (including escapes) and nested containers; do not match
  -- spelling inside a label or a nested object. Repeated fields are refused.
  function T.arrayfield(raw, field)
    local i, depth, found = 1, 0, false
    while i <= #raw do
      local ch = string.sub(raw, i, i)
      if ch == '"' then
        local start = i
        i = i + 1
        while i <= #raw do
          local v = string.sub(raw, i, i)
          if v == '\\' then i = i + 2
          elseif v == '"' then break
          else i = i + 1 end
        end
        if depth == 1 then
          local colon = string.find(raw, '%S', i + 1)
          if colon and string.sub(raw, colon, colon) == ':' and cjson.decode(string.sub(raw, start, i)) == field then
            local value = string.find(raw, '%S', colon + 1)
            if found or not value or string.sub(raw, value, value) ~= '[' then return false end
            found = true
          end
        end
      elseif ch == '{' or ch == '[' then depth = depth + 1
      elseif ch == '}' or ch == ']' then depth = depth - 1 end
      i = i + 1
    end
    return found
  end
  -- T.jsonnumber(s): s is one JSON number token (RFC 8259), nothing else.
  function T.jsonnumber(s)
    local i = 1
    if string.sub(s, i, i) == '-' then i = i + 1 end
    local c = string.sub(s, i, i)
    if c == '0' then
      i = i + 1
    elseif string.match(c, '^[1-9]$') then
      i = i + #string.match(s, '^%d+', i)
    else
      return false
    end
    if string.sub(s, i, i) == '.' then
      local frac = string.match(s, '^%d+', i + 1)
      if not frac then return false end
      i = i + 1 + #frac
    end
    local e = string.sub(s, i, i)
    if e == 'e' or e == 'E' then
      i = i + 1
      local sign = string.sub(s, i, i)
      if sign == '+' or sign == '-' then i = i + 1 end
      local exp = string.match(s, '^%d+', i)
      if not exp then return false end
      i = i + #exp
    end
    return i == #s + 1
  end
  -- T.repeats(raw): the JSON text names a key twice in one object, comparing
  -- the keys as decoded (m\u0065mbers is members). raw has already decoded.
  function T.repeats(raw)
    local pos, len = 1, #raw
    local function ws()
      while pos <= len do
        local b = string.byte(raw, pos)
        if b == 32 or b == 9 or b == 10 or b == 13 then pos = pos + 1 else break end
      end
    end
    local function str()
      local start = pos
      pos = pos + 1
      while pos <= len do
        -- the next quote or backslash, found in C: the characters between
        -- are the string's own
        local at = string.find(raw, '["\\]', pos)
        if not at then pos = len + 1; break end
        if string.byte(raw, at) == 92 then pos = at + 2
        else
          pos = at + 1
          local ok, decoded = pcall(cjson.decode, string.sub(raw, start, pos - 1))
          return ok and decoded or nil
        end
      end
    end
    local value
    local function container(close, keyed)
      pos = pos + 1
      local seen = {}
      ws()
      if string.sub(raw, pos, pos) == close then pos = pos + 1; return false end
      while pos <= len do
        ws()
        if keyed then
          local key = str()
          if key == nil then return false end
          if seen[key] then return true end
          seen[key] = true
          ws()
          pos = pos + 1 -- the colon
        end
        if value() then return true end
        ws()
        local ch = string.sub(raw, pos, pos)
        pos = pos + 1
        if ch ~= ',' then return false end
      end
      return false
    end
    value = function()
      ws()
      local ch = string.sub(raw, pos, pos)
      if ch == '{' then return container('}', true)
      elseif ch == '[' then return container(']', false)
      elseif ch == '"' then str(); return false end
      while pos <= len and not string.find(string.sub(raw, pos, pos), '[,}%]%s]') do pos = pos + 1 end
      return false
    end
    return value()
  end
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
      local f = T.formula(proj)
      local formula = f ~= nil
      -- a sum(...) column folds as a count does; a pct(...) column pools; a text
      -- column of whole numbers folds sum or max (the fleet's width, ntable.numericText)
      local counts = proj == 'count' or (f ~= nil and f.sum)
      local w = tonumber(width)
      if not T.name(col) or not (valid[proj or ''] or formula) or seen[col] or not T.uint(width) or #width > 19 or
        (#width == 19 and width > '9223372036854775807') or not w or w < 0 or w ~= math.floor(w) or
        not (repair or fold == 'none' or ((fold == 'sum' or fold == 'max') and (counts or proj == 'text')) or
          (fold == 'avg' and counts) or (fold == 'pooled' and formula and not f.sum) or
          (fold == 'union' and proj ~= 'count' and proj ~= 'text' and not formula)) then
        return nil, T.refuse('DEFINITION', col)
      end
      seen[col] = true
      order[#order + 1] = col
      cols[#cols + 1] = {name=col, projection=proj, noset=proj == 'text' or formula, inputs=f and f.inputs}
    end
    if #cols == 0 or table.concat(order, ',') ~= h.order then return nil, T.refuse('DEFINITION') end
    if h.sort and not (string.match(h.sort, '^-?name$') or string.match(h.sort, '^-?label$')) then return nil, T.refuse('DEFINITION', 'sort') end
    -- every column a formula reads is a count column of the table (hidden
    -- or not); FORMULA names the formula, the column and why
    for _, col in ipairs(cols) do
      for _, arg in ipairs(col.inputs or {}) do
        if not seen[arg] then return nil, T.refuse('FORMULA', col.name, arg, 'missing') end
        if not string.match(h['col:' .. arg] or '', '^count:') then
          return nil, T.refuse('FORMULA', col.name, arg, string.match(h['col:' .. arg], '^([^:]+):') or '')
        end
      end
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
  -- T.active(cfg): the epoch a configuration is at: epoch 0 without an epoch
  -- key, else the field of the key's hash.
  function T.active(cfg)
    return cfg.epoch_key == '' and '0' or (redis.call('HGET', cfg.epoch_key, cfg.epoch_field) or '0')
  end
  -- T.exists(name, template): a create whose definition differs from the
  -- saved one. A dropped table (absent at its active epoch) keeps its
  -- definition until drop --definition, and set refuses it, so its refusal
  -- carries the epoch and the saved definition: the client names the create
  -- that brings it back and the drop --definition that forgets it.
  function T.exists(name, template)
    local active = T.active(T.config(template))
    if redis.call('HGET', T.prefix(name, active) .. ':definition', '_present') ~= '0' then return T.refuse('EXISTS') end
    return T.refuse('EXISTS', 'DROPPED', active, unpack(T.flat(template)))
  end
  -- T.open(name, fields, historical, repair, orphan): the table's definition
  -- as the store holds it. A table that is gone while its identity hash is
  -- left (the orphan: an earlier build's drop --definition kept the identity)
  -- is refused ORPHAN (naming the epoch its remedy is written at), and opens,
  -- empty, only for the verb that removes it (orphan true: drop --definition).
  -- A table created where the rows of an earlier table of the name are left at
  -- its epoch is refused RESIDUE, naming the keys; it never adopts them.
  function T.open(name, fields, historical, repair, orphan)
    if not T.name(name) then return nil, T.refuse('NAME') end
    local key = 'table:' .. name
    local template = T.hash(key)
    local identity = T.hash(key .. ':identity')
    if fields then
      local _, err = T.shape(fields)
      if err then return nil, err end
      if next(identity) and not T.sameconfig(identity, fields) then
        if next(template) then return nil, T.refuse('CONFIG') end
        local at = T.active(T.config(identity))
        if not T.uint(at) then return nil, T.refuse('EPOCH', at) end
        return nil, T.refuse('ORPHAN', at)
      end
      if next(template) then
        for k, v in pairs(fields) do
          if k ~= 'created_at' and k ~= 'epoch_key' and k ~= 'epoch_field' and k ~= 'member_prefix' and template[k] ~= v then
            return nil, T.exists(name, template)
          end
        end
        if not T.sameconfig(template, fields) then return nil, T.exists(name, template) end
      end
    end
    local h = next(template) and template or fields
    local cfg = T.config(h or identity)
    local active = T.active(cfg)
    if not T.uint(active) then return nil, T.refuse('EPOCH', active) end
    local epoch = historical or active
    if not T.uint(epoch) then return nil, T.refuse('EPOCH', tostring(epoch)) end
    local prefix = T.prefix(name, epoch)
    if fields and not next(template) and not historical then
      local stray = {}
      for _, k in ipairs({prefix .. ':rows', prefix .. ':props'}) do
        if redis.call('EXISTS', k) == 1 then stray[#stray + 1] = k end
      end
      if #stray > 0 then return nil, T.refuse('RESIDUE', active, unpack(stray)) end
    end
    local snap = T.hash(prefix .. ':definition')
    if historical and snap.order then
      h = {}
      for k, v in pairs(snap) do if string.sub(k, 1, 1) ~= '_' then h[k] = v end end
      cfg = T.config(h)
    elseif historical and epoch ~= active then
      -- the table must exist before its epoch can be called ahead
      if next(template) and T.uintgt(epoch, active) then return nil, T.refuse('EPOCHAHEAD', epoch, active) end
      return nil, T.refuse('NOTABLE')
    end
    local cols, err
    local orphaned = not h and next(identity) ~= nil
    if orphaned then
      if not orphan then return nil, T.refuse('ORPHAN', active) end
      h, cols = {}, {}
    else
      if not h then return nil, T.refuse('NOTABLE') end
      cols, err = T.shape(h, repair)
      if not cols then return nil, err end
    end
    local revision = historical and snap._revision or redis.call('HGET', key .. ':revision', 'n')
    revision = revision or '0'
    if not T.uint(revision) then return nil, T.refuse('REVISION', revision) end
    return {name=name, key=key, prefix=prefix, epoch=epoch, active=active, revision=revision,
      h=h, cols=cols, ncols=#cols, cfg=cfg, snap=snap, present=snap._present ~= '0',
      newtemplate=not next(template) and not orphaned, newidentity=not next(identity), commands={}, cells={}, members={}}
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
  -- T.opskey(name): the hash of a table's operation records, every epoch in one
  -- key so that a drop removes them all in one bounded step. Its fields are
  -- <epoch>:<operation id>, its values the records (JSON).
  function T.opskey(name) return 'table:' .. name .. ':ops' end
  function T.rowkey(d, row) return d.prefix .. ':row:' .. row end
  function T.cellkey(d, row, col) return d.prefix .. ':cell:' .. row .. ':' .. col end
  function T.rowskey(d) return d.prefix .. ':rows' end
  -- T.propskey(d): the table's properties at the epoch (a hash of name ->
  -- value): L1-CONTRACT-AMENDMENT-PROPERTY-2026-09-30 section 4.
  function T.propskey(d) return d.prefix .. ':props' end
  function T.col(d, name)
    for _, col in ipairs(d.cols) do if col.name == name then return col end end
  end
  function T.cell(d, row, col, write)
    if not redis.call('ZSCORE', T.rowskey(d), row) then return nil, T.refuse('NOROW', T.excerpt(row)) end
    local c = T.col(d, col)
    if not c then return nil, T.refuse('NOCOL', T.excerpt(row), T.excerpt(col)) end
    if c.noset then return nil, T.refuse('TEXT', T.excerpt(row), T.excerpt(col)) end
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
  local command_expected_type = {
    ZADD = 'zset',
    ZREM = 'zset',
    HSET = 'hash',
    HDEL = 'hash',
    SADD = 'set',
    SREM = 'set',
    XADD = 'stream',
  }
  function T.check_types(commands)
    local key_kinds = {}
    for _, cmd in ipairs(commands) do
      local op = cmd[1]
      local key = cmd[2]
      if op == 'DEL' or op == 'UNLINK' then
        key_kinds[key] = 'none'
      else
        local want = command_expected_type[op]
        if want then
          local kind = key_kinds[key]
          if not kind then
            local t = redis.call('TYPE', key)
            kind = (type(t) == 'table' and t.ok) and t.ok or t
            key_kinds[key] = kind
          end
          if kind ~= 'none' and kind ~= want then
            return T.refuse('WRONGTYPE', key, kind, want)
          end
          if kind == 'none' then
            key_kinds[key] = want
          end
        end
      end
    end
    return nil
  end
  function T.memberkey(d, id) return d.cfg.member_prefix .. id end
  function T.member(d, id)
    if not T.word(id) then return nil, nil, T.refuse('MEMBER', 'a member id is a nonempty string without control characters') end
    local mkey = T.memberkey(d, id)
    local flat = redis.pcall('HGETALL', mkey)
    if type(flat) == 'table' and flat.err then
      if string.find(flat.err, 'WRONGTYPE') then return nil, nil, T.refuse('WRONGTYPE', mkey, T.kind(mkey), 'hash', id) end
      T.rethrow(flat)
    end
    local h = {}
    for i = 1, #flat, 2 do h[flat[i]] = flat[i + 1] end
    local exists = next(h) ~= nil
    local epoch = h.epoch or '0'
    if exists and epoch ~= d.epoch then return nil, nil, T.refuse('MEMBEREPOCH', id, epoch, d.epoch) end
    return h, exists
  end
  -- T.member_head(d, id): a batch's view of a member: whether the record exists
  -- and its epoch, revision and place, read by name. Any other field is read on
  -- first use, by name (member_field in T.apply), so what a batch reads is what
  -- its entries name and not what the record holds; T.limits.batch_value_bytes
  -- bounds those reads.
  function T.member_head(d, id)
    if not T.word(id) then return nil, nil, T.refuse('MEMBER', 'a member id is a nonempty string without control characters') end
    local mkey = T.memberkey(d, id)
    local n = redis.pcall('HLEN', mkey)
    if type(n) == 'table' and n.err then
      if string.find(n.err, 'WRONGTYPE') then return nil, nil, T.refuse('WRONGTYPE', mkey, T.kind(mkey), 'hash', id) end
      T.rethrow(n)
    end
    local h = {}
    if n == 0 then return h, false end
    local named = redis.call('HMGET', mkey, 'epoch', 'revision', 'place:' .. d.name)
    if named[1] then h.epoch = named[1] end
    if named[2] then h.revision = named[2] end
    if named[3] then h['place:' .. d.name] = named[3] end
    if (named[1] or '0') ~= d.epoch then return nil, nil, T.refuse('MEMBEREPOCH', id, named[1] or '0', d.epoch) end
    return h, true
  end
  function T.advance_member_rev(record, exists)
    if not exists then return '1' end
    return T.next(record.revision or '0')
  end
  function T.record(d, id, place, record, exists)
    -- Legacy epoch-zero records acquire an explicit immutable epoch on first
    -- placement. Other record fields belong to their existing owner.
    if not record.epoch then T.stage(d, 'HSET', T.memberkey(d, id), 'epoch', d.epoch) end
    local next_rev = T.advance_member_rev(record, exists)
    if not next_rev then return T.refuse('OVERFLOW', id) end
    if place then T.stage(d, 'HSET', T.memberkey(d, id), 'place:' .. d.name, place, 'revision', next_rev)
    else
      T.stage(d, 'HDEL', T.memberkey(d, id), 'place:' .. d.name)
      T.stage(d, 'HSET', T.memberkey(d, id), 'revision', next_rev)
    end
  end
  function T.change(d, id, from, to, score)
    d.members[#d.members + 1] = {id=id, from=from or '', to=to or '', score=score or ''}
    if from then d.cells[from] = true end
    if to then d.cells[to] = true end
  end
  -- T.unindexed(d, id, tag): the id is in an owned set of the table that no record
  -- places it in. A caller that reports it as a batch or a read set passes a tag,
  -- appended to the refusal, so the reader knows no record exists.
  function T.unindexed(d, id, tag)
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      local h = T.hash(T.rowkey(d, row))
      for _, col in ipairs(d.cols) do
        local key = T.cellkey(d, row, col.name)
        local res = redis.pcall('ZSCORE', key, id)
        if type(res) == 'table' and res.err then
          if string.find(res.err, 'WRONGTYPE') then
            local t = redis.call('TYPE', key)
            local kind = (type(t) == 'table' and t.ok) and t.ok or t
            return T.refuse('WRONGTYPE', key, kind, 'zset')
          end
          T.rethrow(res)
        end
        if res then
          local refusal = T.refuse('DRIFT', row, col.name, id)
          if tag then refusal[#refusal + 1] = tag end
          return refusal
        end
      end
    end
  end
  function T.check_placement(d, id, expected_place)
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      for _, col in ipairs(d.cols) do
        local place = T.place(row, col.name)
        if place ~= expected_place then
          local key = T.cellkey(d, row, col.name)
          local res = redis.pcall('ZSCORE', key, id)
          if type(res) == 'table' and res.err then
            if string.find(res.err, 'WRONGTYPE') then
              local t = redis.call('TYPE', key)
              local kind = (type(t) == 'table' and t.ok) and t.ok or t
              return T.refuse('WRONGTYPE', key, kind, 'zset')
            end
            T.rethrow(res)
          end
          if res then
            return T.refuse('DRIFT', row, col.name, id)
          end
        end
      end
    end
    return nil
  end
  -- T.place_index(d, ids): which of the ids each owned cell of the table
  -- holds, read in one pass over the cells (one ZMSCORE of every id per
  -- cell), for T.index_drift to answer T.check_placement and T.unindexed
  -- from. A read set or a batch checks each of its members against every cell
  -- of the table: one pass for all of them, not one per member (the owner's
  -- rule of 2026-09-30, "there is NO REASON to ever do a row at a time"). The
  -- cells, the order they are looked at and the refusals are the ones the
  -- per-member checks give: a cell of the wrong type is named when a member's
  -- check reaches it, as T.check_placement names it.
  function T.place_index(d, ids)
    -- where[id] is the places (in cell order) of the cells holding the id;
    -- wrong the places of the cells of the wrong type; row_of each cell's row
    -- index, for the row hashes T.unindexed reads
    local idx = {cells = {}, d = d, hashed = {}, where = {}, wrong = {}, rows = {}}
    local rows = redis.call('ZRANGE', T.rowskey(d), 0, -1)
    for r, row in ipairs(rows) do
      idx.rows[r] = row
      for _, col in ipairs(d.cols) do
        local key = T.cellkey(d, row, col.name)
        local cell = {row = row, r = r, col = col.name, place = T.place(row, col.name), key = key}
        idx.cells[#idx.cells + 1] = cell
        local at = #idx.cells
        for start = 1, #ids, 1000 do
          local argv = {'ZMSCORE', key}
          for i = start, math.min(start + 999, #ids) do argv[#argv + 1] = ids[i] end
          local res = redis.pcall(unpack(argv))
          if type(res) == 'table' and res.err then
            if not string.find(res.err, 'WRONGTYPE') then T.rethrow(res) end
            local t = redis.call('TYPE', key)
            cell.wrongtype = (type(t) == 'table' and t.ok) and t.ok or t
            idx.wrong[#idx.wrong + 1] = at
            break
          end
          for i, score in ipairs(res) do
            if score then
              local id = argv[i + 2]
              local w = idx.where[id]
              if not w then w = {}; idx.where[id] = w end
              w[#w + 1] = at
            end
          end
        end
      end
    end
    return idx
  end
  -- T.index_drift(idx, id, expected, tag): T.check_placement (expected is the
  -- member's place) or T.unindexed (expected nil, with its tag) over the
  -- index: the first cell, in row and column order, that holds the id and is
  -- not its place, or that is of the wrong type; found from the cells that
  -- hold the id, not by a walk of every cell.
  function T.index_drift(idx, id, expected, tag)
    local first
    for _, at in ipairs(idx.where[id] or {}) do
      if idx.cells[at].place ~= expected then first = at; break end
    end
    for _, at in ipairs(idx.wrong) do
      if idx.cells[at].place ~= expected then
        if not first or at < first then first = at end
        break
      end
    end
    if expected == nil then
      -- T.unindexed reads each row's hash before its cells: a row that is
      -- not a hash is refused as it refuses it
      local last = first and idx.cells[first].r or #idx.rows
      for r = 1, last do
        if not idx.hashed[r] then
          T.hash(T.rowkey(idx.d, idx.rows[r]))
          idx.hashed[r] = true
        end
      end
    end
    if not first then return nil end
    local cell = idx.cells[first]
    if cell.wrongtype then return T.refuse('WRONGTYPE', cell.key, cell.wrongtype, 'zset') end
    local refusal = T.refuse('DRIFT', cell.row, cell.col, id)
    if tag then refusal[#refusal + 1] = tag end
    return refusal
  end
  -- T.cell_once(d, row, col, write): T.cell, looked up once per call of a
  -- function and kept: a read set or a batch names a cell for each member,
  -- and the rows and their bindings do not change before its writes.
  function T.cell_once(d, row, col, write)
    d.cell_memo = d.cell_memo or {}
    local k = row .. '\0' .. col .. '\0' .. (write and '1' or '0')
    local m = d.cell_memo[k]
    if not m then
      local cell, why = T.cell(d, row, col, write)
      m = {cell = cell, why = why}
      d.cell_memo[k] = m
    end
    return m.cell, m.why
  end
  function T.rowfields(d, row, spec)
    if not T.row(row) or type(spec) ~= 'table' then return nil, T.refuse('ROW') end
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
      if not new and col.projection == 'text' and old['text:' .. col.name] and old['text:' .. col.name] ~= '' then
        return T.refuse('OCCUPIEDVALUE', row, col.name)
      end
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
          local rerr = T.record(d, id, nil, record, exists)
          if rerr then return rerr end
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
  function T.sorted(d, by, desc, cols)
    local rows, seen, keys = {}, {}, {}
    for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
      if not (d.removed and d.removed[row]) then rows[#rows + 1] = row; seen[row] = true end
    end
    for _, row in ipairs(T.sortedkeys(d.touched or {})) do
      if not seen[row] then rows[#rows + 1] = row; seen[row] = true end
    end
    local col
    if by ~= 'name' and by ~= 'label' then
      for _, candidate in ipairs(cols or d.cols) do if candidate.name == by then col = candidate end end
      if not col then return nil, T.nocol(by) end
      if col.projection ~= 'count' and col.projection ~= 'text' then return nil, T.refuse('SORTKEY', by) end
    end
    for _, row in ipairs(rows) do
      local h = (d.touched and d.touched[row]) or (d.reshaped and d.reshaped[row]) or T.hash(T.rowkey(d, row))
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
  -- T.rank(d, rows): write ranks 1..n only when the stored order differs.
  -- Bound each ZADD to 256 rows so neither ACL preflight nor execution
  -- unpacks an unbounded argument list. Every chunk is still staged;
  -- T.finish validates all of them before writing and emits one receipt.
  function T.rank(d, rows)
    local have = redis.call('ZRANGE', T.rowskey(d), 0, -1)
    local same = #have == #rows
    for i, row in ipairs(rows) do if have[i] ~= row then same = false end end
    if same or #rows == 0 then return end
    local cmd = {'ZADD', T.rowskey(d)}
    for i, row in ipairs(rows) do
      cmd[#cmd + 1] = tostring(i); cmd[#cmd + 1] = row
      for _, col in ipairs(d.cols) do d.cells[T.place(row, col.name)] = true end
      if i % 256 == 0 then
        d.commands[#d.commands + 1] = cmd
        cmd = {'ZADD', T.rowskey(d)}
      end
    end
    if #cmd > 2 then d.commands[#d.commands + 1] = cmd end
  end
  -- Untrimmed change streams are the receipt. All validation (including
  -- stream/revision bounds and ACLs) precedes all writes; XADD is last.
  function T.finish(d, verb, args, opts, reply)
    local after = T.next(d.revision)
    if not after then return T.refuse('REVISION', d.revision) end
    -- the size of a table: a definition is written by one HSET and a row count
    -- is read by whole-table verbs
    -- (a table already over the bound may shrink or stay, never grow)
    if d.newtemplate or (d.definition_changed and #d.cols > d.ncols) then
      local over = T.over('columns', #d.cols)
      if over then return over end
    end
    if d.newrows then
      local over = T.over('rows', redis.call('ZCARD', T.rowskey(d)) + d.newrows)
      if over then return over end
    end
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
    -- opts.outcome names the outcome when the caller judges it (a batch whose
    -- entries change nothing is a noop although it stages a revision step).
    local outcome = opts.outcome or (#d.commands == 0 and not d.newtemplate and not d.definition_changed and d.snap._present ~= '0' and 'noop' or 'changed')
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
    -- opts.wireargs replaces the event's args for a caller whose arguments do
    -- not end in the options JSON.
    local wireargs = opts.wireargs
    if not wireargs then
      wireargs = {}
      for i = 1, #args - 1 do wireargs[#wireargs + 1] = args[i] end
    end
    local event = {'XADD', stream, '*', 'verb', verb, 'args', cjson.encode(wireargs),
      'epoch', d.epoch, 'rev_before', d.revision, 'rev_after', after, 'actor', opts.actor or '',
      'fence', opts.fence or '', 'idem', opts.idem or '', 'cells', #cells == 0 and '[]' or cjson.encode(cells),
      'members', #d.members == 0 and '[]' or cjson.encode(d.members), 'outcome', outcome}
    local members_at = #event - 2
    local delta_at
    if d.renamed_keys then event[#event + 1] = 'renamed_keys'; event[#event + 1] = cjson.encode(d.renamed_keys) end
    -- opts.event_extra is a flat list of field, value pairs appended to the event.
    if opts.event_extra then
      for _, v in ipairs(opts.event_extra) do event[#event + 1] = v end
      for i = #event - #opts.event_extra + 1, #event, 2 do
        if event[i] == 'batch_delta' then delta_at = i + 1 end
      end
    end
    T.stage(d, unpack(event))
    for _, cmd in ipairs(d.commands) do
      if not redis.acl_check_cmd(unpack(cmd)) then return T.refuse('NOPERM', cmd[1], cmd[2]) end
    end
    local type_err = T.check_types(d.commands)
    if type_err then return type_err end
    local id, delta
    for i, cmd in ipairs(d.commands) do
      if i == #d.commands and opts.resolve then
        -- the event is the last write: its members and delta are settled now
        delta = opts.resolve()
        cmd[members_at] = #d.members == 0 and '[]' or cjson.encode(d.members)
        if delta_at then cmd[delta_at] = delta end
      end
      id = redis.call(unpack(cmd))
    end
    local before = d.revision
    d.revision = after
    if type(reply) == 'function' then reply = reply() end
    local receipt = {'RECEIPT', id, d.epoch, before, after, outcome}
    -- opts.receipt_extra is a seventh receipt element (a batch's delta).
    if delta or opts.receipt_extra then receipt[7] = delta or opts.receipt_extra end
    reply[#reply + 1] = receipt
    -- opts.record runs last, after every staged write, with the finished reply
    -- (a batch keeps its operation record here, which holds the receipt).
    if opts.record then opts.record(reply, id, before, after, outcome) end
    return reply
  end
  function T.write(verb, argc, handler, declaration)
    return function(keys, args)
      if (argc >= 0 and #args ~= argc) or (argc < 0 and #args < -argc) then return T.refuse('ARGS', verb) end
      local opts = T.decode(args[#args])
      if not opts or not T.uint(opts.epoch) then return T.refuse('EPOCH', 'requested epoch required') end
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
      local d, err = T.open(args[1], fields, nil, edit and edit.columns ~= nil, verb == 'drop_definition')
      if not d then return err end
      local missing = not d.present and not declaration and verb ~= 'drop_definition'
      if opts.epoch ~= d.active then
        -- a table that does not exist is missing whatever epoch is asked for
        if missing and T.uintgt(opts.epoch, d.active) then return T.refuse('NOTABLE') end
        if T.uintgt(opts.epoch, d.active) then return T.refuse('EPOCHAHEAD', opts.epoch, d.active) end
        return T.refuse('STALE', opts.epoch, d.active)
      end
      if missing then return T.refuse('NOTABLE') end
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
      d.newrows = (d.newrows or 0) + 1
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
      if type(key) ~= 'number' or key < 1 or key > #values or key ~= math.floor(key) or not T.row(value) or seen[value] then return nil end
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
    if not T.row(row) or type(values) ~= 'table' or not next(values) then return nil, T.refuse('ROW') end
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
    d.reshaped = {}
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
          h['text:' .. col.name] = nil
        end
        if not replacement or replacement.noset then
          T.stage(d, 'HDEL', T.rowkey(d, row), 'key:' .. col.name)
          h['key:' .. col.name] = nil
        end
        d.cells[T.place(row, col.name)] = true
      end
      for _, col in ipairs(cols) do
        if not oldcols[col.name] then
          local members = redis.call('ZRANGE', T.cellkey(d, row, col.name), 0, -1)
          if #members > 0 then return T.refuse('OCCUPIED', row, col.name, members) end
          T.stage(d, 'HDEL', T.rowkey(d, row), 'text:' .. col.name, 'key:' .. col.name)
          h['text:' .. col.name], h['key:' .. col.name] = nil, nil
        end
        d.cells[T.place(row, col.name)] = true
      end
      d.reshaped[row] = h
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
          local f = T.formula(string.match(h['col:' .. col] or '', '^([^:]+):'))
          for _, arg in ipairs(f and f.inputs or {}) do
            if arg == gone then return nil, T.refuse('DEPENDS', gone, col) end
          end
        end
      end
      if #list == 0 then return nil, T.refuse('LASTCOL', gone) end
      -- Report every owned member that blocks this column removal in the
      -- same refusal. External bound sets remain owned by their source.
      local blockers = {}
      for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
        local members = redis.call('ZRANGE', T.cellkey(d, row, gone), 0, -1)
        if #members > 0 then blockers[#blockers + 1] = {row, gone, members} end
      end
      if #blockers == 1 then return nil, T.refuse('OCCUPIED', unpack(blockers[1])) end
      if #blockers > 1 then return nil, T.refuse('OCCUPIEDCELLS', blockers) end
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
      if h.order ~= d.h.order then
        for _, row in ipairs(redis.call('ZRANGE', T.rowskey(d), 0, -1)) do
          for _, col in ipairs(d.cols) do d.cells[T.place(row, col.name)] = true end
        end
      end
    end
    -- A combined shape edit and sort reads the shape and row fields that
    -- this call will leave, after all data-preservation checks pass.
    local cols, err = T.shape(h)
    if not cols then return nil, err end
    if spec.columns or spec.col_add or spec.col_del then
      local loss = T.reshape(d, cols)
      if loss then return nil, loss end
    end
    -- the rows' order: one row moved, some rows named first, or all sorted
    local rows
    if spec.row_sort ~= nil then
      local o = spec.row_sort
      if type(o) ~= 'table' then return nil, T.refuse('DEFINITION') end
      if o.manual then h.sort = nil
      else
        if type(o.by) ~= 'string' or o.by == '' then return nil, T.refuse('SORTKEY', tostring(o.by)) end
        if o.keep and o.by ~= 'name' and o.by ~= 'label' then return nil, T.refuse('SORTKEEP', o.by) end
        local why
        rows, why = T.sorted(d, o.by, o.desc and true or false, cols)
        if not rows then return nil, why end
        if o.keep then h.sort = (o.desc and '-' or '') .. o.by else h.sort = nil end
      end
    end
    if (spec.row_move ~= nil or spec.row_order ~= nil) and h.sort then
      return nil, T.refuse('SORTED', h.sort)
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
      if type(m) ~= 'table' or not T.row(m.row) or (m.ref ~= nil and not T.row(m.ref)) then return nil, T.refuse('ROW') end
      local why
      rows, why = T.reorder(rows or redis.call('ZRANGE', T.rowskey(d), 0, -1), m.row, m.where, m.ref, T.norow)
      if not rows then return nil, why end
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
  -- T.sweep(d): the rows of every epoch before the active one, with their
  -- cells and properties, and the places their members' records hold: what a
  -- drop --definition removes beyond the active epoch, so no later table of
  -- the name has rows to adopt. The epochs are walked 0 .. active - 1, under a
  -- bound; the snapshot of each gives the columns whose cells are owned.
  T.sweep_epochs = 1000
  function T.sweep(d)
    if #d.active > 3 then return T.refuse('LIMIT', 'epochs to sweep', T.sweep_epochs, d.active) end
    for n = 0, tonumber(d.active) - 1 do
      local e = tostring(n)
      local prefix = T.prefix(d.name, e)
      local h = {}
      for k, v in pairs(T.hash(prefix .. ':definition')) do
        if string.sub(k, 1, 1) ~= '_' then h[k] = v end
      end
      local cols = h.order and T.shape(h, true) or {}
      for _, row in ipairs(redis.call('ZRANGE', prefix .. ':rows', 0, -1)) do
        local rh = T.hash(prefix .. ':row:' .. row)
        for _, col in ipairs(cols) do
          local bound = rh['key:' .. col.name]
          if not col.noset and (not bound or bound == '') then
            local key = prefix .. ':cell:' .. row .. ':' .. col.name
            for _, id in ipairs(redis.call('ZRANGE', key, 0, -1)) do
              local mkey = d.cfg.member_prefix .. id
              local rec = T.hash(mkey)
              if rec['place:' .. d.name] == T.place(row, col.name) and (rec.epoch or '0') == e then
                local after = T.next(rec.revision or '0')
                if not after then return T.refuse('OVERFLOW', id) end
                T.stage(d, 'HDEL', mkey, 'place:' .. d.name)
                T.stage(d, 'HSET', mkey, 'revision', after)
              end
            end
            T.stage(d, 'DEL', key)
          end
        end
        T.stage(d, 'DEL', prefix .. ':row:' .. row)
      end
      T.stage(d, 'DEL', prefix .. ':rows')
      T.stage(d, 'DEL', prefix .. ':props')
    end
  end
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
    -- the table's properties at the epoch go with its rows (L1 contract
    -- amendment, table properties)
    T.stage(d, 'DEL', T.propskey(d))
    if op == 'drop' or op == 'drop_definition' then
      d.present = false
      -- The table's operation records, of every epoch, are one key (T.opskey):
      -- a table created again under the name is a new table, and no operation
      -- of the old one replays against it.
      T.stage(d, 'DEL', T.opskey(d.name))
    end
    if op == 'drop_definition' then
      local swept = T.sweep(d)
      if swept then return nil, swept end
      redis.call('SCARD', 'tables')
      T.stage(d, 'DEL', d.key)
      -- the table's identity goes with its template, so a table created again
      -- under the name is a new table with its own configuration (the orphan
      -- an earlier build left is removed here, too); T.finish does not write
      -- it back
      T.stage(d, 'DEL', d.key .. ':identity')
      d.newidentity = false
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
    local count = redis.pcall('ZCARD', src.key)
    if type(count) == 'table' and count.err then
      if string.find(count.err, 'WRONGTYPE') then return nil, T.refuse('WRONGTYPE', src.key, T.kind(src.key), 'zset') end
      T.rethrow(count)
    end
    local first, dst = 5, nil
    if op == 'add' then
      local score = tonumber(args[4])
      if not score or score ~= score or score == math.huge or score == -math.huge then return nil, T.refuse('SCORE') end
    elseif op == 'remove' then first = 4
    else
      dst, err = T.cell(d, row, args[4], true)
      if not dst then return nil, err end
      count = redis.pcall('ZCARD', dst.key)
      if type(count) == 'table' and count.err then
        if string.find(count.err, 'WRONGTYPE') then return nil, T.refuse('WRONGTYPE', dst.key, T.kind(dst.key), 'zset') end
        T.rethrow(count)
      end
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
        local rerr = T.record(d, id, here, record, exists)
        if rerr then return nil, rerr end
        T.change(d, id, nil, here, args[4])
        count = count + 1
      elseif op == 'remove' then
        local score = redis.call('ZSCORE', src.key, id)
        if placed ~= here then
          if score then return nil, T.refuse('DRIFT', row, col, id) end
        else
          if not score then return nil, T.refuse('DRIFT', row, col, id) end
          T.stage(d, 'ZREM', src.key, id)
          local rerr = T.record(d, id, nil, record, exists)
          if rerr then return nil, rerr end
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
          local rerr = T.record(d, id, T.place(row, args[4]), record, exists)
          if rerr then return nil, rerr end
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
      if not d then
        if err[2] == 'NOTABLE' then return T.refuse('VIEWTABLE', name) end
        return err
      end
      if #names == 0 and args[4] ~= '' then
        local summary = T.col(d, args[4])
        if not summary or summary.projection ~= 'count' then return T.refuse('SUMMARY', name, args[4]) end
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
  -- A view's state text: shown alone as the summary line while set, in place
  -- of the counts (the machine that fills the view is STOPPED, say). '' clears
  -- it. A view set leaves it as it is; only this function writes it.
  redis.register_function('ns_view_state', function(keys, args)
    if #args ~= 2 or not T.name(args[1]) then return T.refuse('ARGS', 'view_state') end
    if args[2] ~= '' and (not T.word(args[2]) or #args[2] > 64) then return T.refuse('ARGS', 'state') end
    local key = 'view:' .. args[1]
    local h = T.hash(key)
    if not next(h) then return T.refuse('NOVIEW', args[1]) end
    local cmd = {'HSET', key, 'state', args[2]}
    if args[2] == '' then cmd = {'HDEL', key, 'state'} end
    if not redis.acl_check_cmd(unpack(cmd)) then return T.refuse('NOPERM', cmd[1], cmd[2]) end
    redis.call(unpack(cmd))
    return {'OK'}
  end)
  redis.register_function{function_name = 'ns_view_get', flags = {'no-writes'}, callback = function(keys, args)
    if #args ~= 1 or not T.name(args[1]) then return T.refuse('ARGS', 'view_get') end
    local h = redis.call('HGETALL', 'view:' .. args[1])
    if #h == 0 then return T.refuse('NOVIEW', args[1]) end
    return {'OK', h}
  end}
  redis.register_function{function_name = 'ns_view_list', flags = {'no-writes'}, callback = function(keys, args)
    if #args ~= 0 then return T.refuse('ARGS', 'view_list') end
    local names = redis.call('SMEMBERS', 'views')
    table.sort(names)
    return {'OK', names}
  end}
  redis.register_function('ns_view_del', function(keys, args)
    if #args ~= 1 or not T.name(args[1]) then return T.refuse('ARGS', 'view_del') end
    local key = 'view:' .. args[1]
    local h = T.hash(key)
    redis.call('SCARD', 'views')
    local commands = {{'DEL', key}, {'SREM', 'views', args[1]}}
    for _, cmd in ipairs(commands) do if not redis.acl_check_cmd(unpack(cmd)) then return T.refuse('NOPERM', cmd[1], cmd[2]) end end
    for _, cmd in ipairs(commands) do redis.call(unpack(cmd)) end
    return {'OK', next(h) and 1 or 0}
  end)
  -- The member's indexed owned location, verified against the same snapshot's
  -- cell. External bindings are views and do not own placement records.
  redis.register_function{function_name = 'ns_table_member_find', flags = {'no-writes'}, callback = function(keys, args)
    if #args ~= 2 then return T.refuse('ARGS', 'member_find') end
    local d, err = T.def(args[1])
    if not d then return err end
    local h, exists, why = T.member(d, args[2])
    if why then return why end
    local state, row, col = exists and 'unplaced' or 'missing', '', ''
    local place = h['place:' .. d.name]
    if place then
      row, col = string.match(place, '^(.*):([^:]+)$')
      if not row or not T.word(row) then return T.refuse('DRIFT', args[2], place) end
      local cell = T.cell(d, row, col, true)
      if not cell or not redis.call('ZSCORE', cell.key, args[2]) then return T.refuse('DRIFT', args[2], place) end
      state = 'placed'
    end
    return {'MEMBER', d.epoch, d.revision, state, row, col}
  end}
  redis.register_function('ns_table_member_create', T.write('member_create', 3, function(d, args)
    local record, exists, err = T.member(d, args[2])
    if err then return nil, err end
    if exists then return nil, T.refuse('MEMBEREXISTS', args[2]) end
    T.stage(d, 'HSET', T.memberkey(d, args[2]), 'epoch', d.epoch, 'revision', '1')
    T.change(d, args[2], nil, nil, nil)
    return {'OK'}
  end))
  redis.register_function('ns_table_bind', T.write('bind', 3, function(d, args, spec)
    if type(spec.rows) ~= 'table' or not T.arrayfield(args[2], 'rows') then return nil, T.refuse('ROW') end
    -- a bind leaves exactly the rows it names (a repeat is refused below), so the
    -- count it names is the size the table takes; finish counts only rows added
    local over = T.over('rows', #spec.rows)
    if over then return nil, over end
    local old, keep = redis.call('ZRANGE', T.rowskey(d), 0, -1), {}
    d.touched, d.removed = {}, {}
    for i, row in ipairs(spec.rows) do
      if type(row) ~= 'table' or not T.row(row.key) then return nil, T.refuse('ROW') end
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
      -- Standing order is ranked once in finish from the final row set.
      -- Staging input ranks here would override even an unchanged sort.
      if not d.h.sort then T.stage(d, 'ZADD', T.rowskey(d), i, row.key) end
      d.touched[row.key] = h
      for _, col in ipairs(d.cols) do d.cells[T.place(row.key, col.name)] = true end
    end
    for _, row in ipairs(old) do
      if not keep[row] then
        local prior = T.hash(T.rowkey(d, row))
        local loss = T.keep_owned(d, row, prior, nil)
        if loss then return nil, loss end
        local drift = T.remove(d, row, prior)
        if drift then return nil, drift end
        d.removed[row] = true
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
    local _, props_flat = T.hash(T.propskey(d))
    local out = {'TABLE', T.flatdef(d), {}, props_flat}
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
            if type(count) == 'table' and count.err then value = {'UNREAD', count.err, key, string.find(count.err, 'WRONGTYPE') and T.kind(key) or ''}
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

  -- A read set's request is one of three shapes, each nonempty:
  --   {"members": ["id", ...]}
  --   {"selection": [{"row": "r", "col": "c"}, ...]}
  --   ["id", ...]
  -- Anything else refuses; it is never answered as an empty set.
  function T.read_set(keys, args)
    if #args < 2 or #args > 3 then return T.refuse('ARGS', 'read_set wants a table, a scope and at most an epoch') end
    local table_name = args[1]
    local scope = T.decode(args[2])
    if not scope then return T.refuse('ARGS', 'read_set scope must be a JSON object or array') end
    if T.repeats(args[2]) then return T.refuse('ARGS', 'read_set request names a key twice') end
    local shape = 'read_set scope must be {"members": [...]}, {"selection": [...]} or a nonempty array of member ids'
    local members, selection
    if scope.members ~= nil or scope.selection ~= nil then
      for k in pairs(scope) do
        if k ~= 'members' and k ~= 'selection' then return T.refuse('ARGS', 'read_set scope has an unknown key') end
      end
      if scope.members ~= nil and scope.selection ~= nil then
        return T.refuse('ARGS', 'read_set scope holds members or selection, not both')
      end
      members, selection = scope.members, scope.selection
    elseif #scope > 0 and next(scope, #scope) == nil then
      members = scope
    else
      return T.refuse('ARGS', shape)
    end
    if members ~= nil and (type(members) ~= 'table' or #members == 0) then
      return T.refuse('ARGS', 'read_set members must be a nonempty array')
    end
    if selection ~= nil and (type(selection) ~= 'table' or #selection == 0) then
      return T.refuse('ARGS', 'read_set selection must be a nonempty array')
    end
    local d, err = T.def(table_name, args[3])
    if not d then return err end
    local target_ids = {}
    local seen_ids = {}
    if members then
      for _, id in ipairs(members) do
        if type(id) ~= 'string' or id == '' then
          return T.refuse('ARGS', 'read_set member ID must be a nonempty string')
        end
        if not seen_ids[id] then
          seen_ids[id] = true
          target_ids[#target_ids + 1] = id
        end
      end
    else
      for _, sel in ipairs(selection) do
        if type(sel) ~= 'table' or type(sel.row) ~= 'string' or type(sel.col) ~= 'string' or sel.row == '' or sel.col == '' then
          return T.refuse('ARGS', 'read_set selection requires row and col strings')
        end
        for k in pairs(sel) do
          if k ~= 'row' and k ~= 'col' then return T.refuse('ARGS', 'read_set selection has an unknown key') end
        end
        local cell, why = T.cell(d, sel.row, sel.col, false)
        if not cell then return why end
        local ms = T.members(cell.key, cell.exclude or '')
        for i = 1, #ms, 2 do
          local id = ms[i]
          if not seen_ids[id] then
            seen_ids[id] = true
            target_ids[#target_ids + 1] = id
          end
        end
      end
    end
    local over = T.over('read_set_members', #target_ids)
    if over then return over end
    local members_out = {}
    local missing = {}
    local idx
    for _, id in ipairs(target_ids) do
      local h, exists, why = T.member(d, id)
      if why then return why end
      if not exists then
        idx = idx or T.place_index(d, target_ids)
        local drift = T.index_drift(idx, id, nil, 'set-only')
        if drift then return drift end
        missing[#missing + 1] = id
      else
        local rev = h.revision or '0'
        local place = h['place:' .. d.name]
        local placed = '0'
        local row, col, score = '', '', '0'
        if place then
          local r, c = string.match(place, '^(.*):([^:]+)$')
          if not r or not c then return T.refuse('DRIFT', id, place) end
          local cell = T.cell_once(d, r, c, false)
          if not cell then return T.refuse('DRIFT', id, place) end
          local s = redis.call('ZSCORE', cell.key, id)
          if not s then return T.refuse('DRIFT', r, c, id) end
          idx = idx or T.place_index(d, target_ids)
          local drift = T.index_drift(idx, id, place)
          if drift then return drift end
          placed = '1'
          row, col = r, c
          score = tostring(s)
        else
          idx = idx or T.place_index(d, target_ids)
          local drift = T.index_drift(idx, id, nil, 'set-only')
          if drift then return drift end
        end
        local fields = {}
        for k, v in pairs(h) do
          if k ~= 'epoch' and k ~= 'revision' and string.sub(k, 1, 6) ~= 'place:' then
            fields[#fields + 1] = k
            fields[#fields + 1] = v
          end
        end
        members_out[#members_out + 1] = {id, rev, placed, row, col, score, fields}
      end
    end
    return {'SET', d.name, d.epoch, d.revision, members_out, missing}
  end

  function T.validate_manifest_json(raw)
    local pos = 1
    local len = #raw

    local function skip_ws()
      while pos <= len do
        local b = string.byte(raw, pos)
        if b == 32 or b == 9 or b == 10 or b == 13 then
          pos = pos + 1
        else
          break
        end
      end
    end

    local function peek()
      skip_ws()
      if pos > len then return nil end
      return string.sub(raw, pos, pos)
    end

    local function next_char()
      skip_ws()
      if pos > len then return nil end
      local ch = string.sub(raw, pos, pos)
      pos = pos + 1
      return ch
    end

    local function parse_string()
      skip_ws()
      if string.sub(raw, pos, pos) ~= '"' then return nil, "expected string" end
      local start = pos
      pos = pos + 1
      while pos <= len do
        -- the next quote or backslash, found in C: the characters between
        -- are the string's own
        local at = string.find(raw, '["\\]', pos)
        if not at then pos = len + 1; break end
        if string.byte(raw, at) == 92 then
          pos = at + 2
        else
          pos = at + 1
          local ok, s = pcall(cjson.decode, string.sub(raw, start, pos - 1))
          if not ok then return nil, "invalid string escape" end
          return s
        end
      end
      return nil, "unterminated string"
    end

    local parse_value, parse_object, parse_array

    parse_value = function(ctx)
      skip_ws()
      local ch = peek()
      if not ch then return nil, "unexpected EOF" end
      if ctx == 'root' or ctx == 'member' or ctx == 'expect' or ctx == 'place' or ctx == 'create' or ctx == 'move' or ctx == 'set' or ctx == 'fields' or ctx == 'field_guard' or ctx == 'props' then
        if ch ~= '{' then
          return nil, "expected object for " .. tostring(ctx)
        end
        return parse_object(ctx)
      elseif ctx == 'members' or ctx == 'unset' or ctx == 'one_of' or ctx == 'prop_absent' then
        if ch ~= '[' then
          return nil, "expected array for " .. tostring(ctx)
        end
        return parse_array(ctx)
      elseif ctx == 'string' then
        if ch ~= '"' then
          return nil, "expected string"
        end
        local s, err = parse_string()
        if err then return nil, err end
        return true
      end

      if ch == '{' then
        return parse_object(ctx)
      elseif ch == '[' then
        return parse_array(ctx)
      elseif ch == '"' then
        local s, err = parse_string()
        if err then return nil, err end
        return true
      elseif ch == 't' or ch == 'f' or ch == 'n' then
        local start = pos
        while pos <= len do
          local c = string.sub(raw, pos, pos)
          if c == ',' or c == '}' or c == ']' or c == ' ' or c == '\t' or c == '\r' or c == '\n' then
            break
          end
          pos = pos + 1
        end
        local lit = string.sub(raw, start, pos - 1)
        if lit ~= "true" and lit ~= "false" and lit ~= "null" then
          return nil, "invalid literal: " .. lit
        end
        return true
      else
        local start = pos
        while pos <= len do
          local c = string.sub(raw, pos, pos)
          if c == ',' or c == '}' or c == ']' or c == ' ' or c == '\t' or c == '\r' or c == '\n' then
            break
          end
          pos = pos + 1
        end
        local num_str = string.sub(raw, start, pos - 1)
        -- The JSON number grammar only: tonumber also reads 0x10, inf and 1e5 spellings JSON does not have.
        if not T.jsonnumber(num_str) then
          return nil, "invalid number"
        end
        if ctx == 'schema' and not string.match(num_str, '^[0-9]+$') then
          return nil, "schema must be an integer"
        end
        return true
      end
    end

    parse_object = function(ctx)
      if next_char() ~= '{' then return nil, "expected {" end
      local seen = {}
      skip_ws()
      if peek() == '}' then
        next_char()
        if ctx == 'member' then
          return nil, "member entry missing expect record"
        end
        return true
      end

      while true do
        local key, err = parse_string()
        if err then return nil, err end

        if seen[key] then
          return nil, "duplicate key: " .. key
        end
        seen[key] = true

        local val_ctx = nil
        if ctx == 'root' then
          local root_keys = {
            schema=true, table=true, epoch=true, expected_table_revision=true,
            operation_id=true, actor=true, members=true,
            props=true, prop_expect=true, prop_absent=true
          }
          if not root_keys[key] then return nil, "unknown field: " .. key end
          if key == 'members' then val_ctx = 'members' end
          if key == 'props' or key == 'prop_expect' then val_ctx = 'props' end
          if key == 'prop_absent' then val_ctx = 'prop_absent' end
          if key == 'schema' then val_ctx = 'schema' end
        elseif ctx == 'member' then
          local member_keys = {
            id=true, expect=true, create=true, move=true, remove=true, set=true, unset=true
          }
          if not member_keys[key] then return nil, "unknown member field: " .. key end
          if key == 'expect' then val_ctx = 'expect'
          elseif key == 'create' then val_ctx = 'create'
          elseif key == 'move' then val_ctx = 'move'
          elseif key == 'set' then val_ctx = 'set'
          elseif key == 'unset' then val_ctx = 'unset'
          end
        elseif ctx == 'expect' then
          local expect_keys = {
            absent=true, revision=true, place=true, fields=true
          }
          if not expect_keys[key] then return nil, "unknown expect field: " .. key end
          if key == 'place' then val_ctx = 'place'
          elseif key == 'fields' then val_ctx = 'fields'
          end
        elseif ctx == 'place' then
          local place_keys = {row=true, col=true}
          if not place_keys[key] then return nil, "unknown place field: " .. key end
        elseif ctx == 'fields' then
          if not T.word(key) then return nil, "invalid field guard name: " .. key end
          val_ctx = 'field_guard'
        elseif ctx == 'field_guard' then
          local guard_keys = {equals=true, absent=true, one_of=true}
          if not guard_keys[key] then return nil, "unknown guard field: " .. key end
          if key == 'one_of' then val_ctx = 'one_of' end
        elseif ctx == 'create' then
          local create_keys = {row=true, col=true, score=true}
          if not create_keys[key] then return nil, "unknown create field: " .. key end
        elseif ctx == 'move' then
          local move_keys = {row=true, col=true, score=true}
          if not move_keys[key] then return nil, "unknown move field: " .. key end
        elseif ctx == 'set' then
          if not T.word(key) then return nil, "invalid field name: " .. key end
          val_ctx = 'string'
        elseif ctx == 'props' then
          if not T.word(key) then return nil, "invalid property name: " .. key end
          val_ctx = 'string'
        else
          return nil, "an object is not allowed here"
        end

        skip_ws()
        if next_char() ~= ':' then return nil, "expected :" end

        local ok, v_err = parse_value(val_ctx)
        if not ok then return nil, v_err end

        skip_ws()
        local sep = peek()
        if sep == ',' then
          next_char()
        elseif sep == '}' then
          next_char()
          break
        else
          return nil, "expected , or } after object property"
        end
      end

      if ctx == 'member' then
        if not seen['id'] then return nil, "member entry missing id" end
        if not seen['expect'] then return nil, "member entry missing expect record" end
      end

      return true
    end

    parse_array = function(ctx)
      if next_char() ~= '[' then return nil, "expected [" end
      skip_ws()
      if peek() == ']' then
        next_char()
        return true
      end

      while true do
        local elem_ctx = nil
        if ctx == 'members' then
          elem_ctx = 'member'
        elseif ctx == 'unset' or ctx == 'one_of' or ctx == 'prop_absent' then
          elem_ctx = 'string'
        end

        local ok, err = parse_value(elem_ctx)
        if not ok then return nil, err end

        skip_ws()
        local sep = peek()
        if sep == ',' then
          next_char()
        elseif sep == ']' then
          next_char()
          break
        else
          return nil, "expected , or ] after array element"
        end
      end
      return true
    end

    skip_ws()
    if peek() ~= '{' then return T.refuse('MANIFEST', "expected root object") end
    local ok, err = parse_object('root')
    if not ok then return T.refuse('MANIFEST', err) end
    skip_ws()
    if pos <= len then return T.refuse('MANIFEST', "trailing characters") end
    return nil
  end

  -- T.static_entries(manifest): every check that needs no store: shape, types,
  -- bounds and combinations, in one pass over the entries. It returns a refusal,
  -- or nil and the counts of entries with changes and guard-only entries.
  -- A refusal here happens before the store is read.
  function T.static_props(manifest)
    local function count(o)
      local n = 0
      for _ in pairs(o) do n = n + 1 end
      return n
    end
    local expected = {}
    for _, key in ipairs({'props', 'prop_expect'}) do
      local o = manifest[key]
      if o ~= nil then
        if type(o) ~= 'table' then return T.refuse('ARGS', key .. ' must be an object') end
        local over = T.over('manifest_props', count(o))
        if over then return over end
        for name, value in pairs(o) do
          if not T.name(name) or type(value) ~= 'string' then return T.refuse('ARGS', key .. ': property names are identifiers and values strings') end
          over = T.over('field_value_bytes', #value)
          if over then return over end
          if key == 'prop_expect' then expected[name] = true end
        end
      end
    end
    local absent = manifest.prop_absent
    if absent ~= nil then
      if type(absent) ~= 'table' then return T.refuse('ARGS', 'prop_absent must be an array') end
      local over = T.over('manifest_props', #absent)
      if over then return over end
      local named = {}
      for _, name in ipairs(absent) do
        if not T.name(name) then return T.refuse('ARGS', 'prop_absent: property names are identifiers') end
        if named[name] or expected[name] then return T.refuse('ARGS', 'prop_absent names a property twice or one prop_expect names') end
        named[name] = true
      end
    end
    return nil
  end
  function T.static_entries(manifest)
    local seen, changed, guards = {}, 0, 0
    -- The table's properties the manifest writes and expects (L1 contract
    -- amendment, table properties, section 4): names, values and counts.
    local props_err = T.static_props(manifest)
    if props_err then return props_err end
    if #manifest.members == 0 and next(manifest.props or {}) == nil and next(manifest.prop_expect or {}) == nil
        and #(manifest.prop_absent or {}) == 0 then
      return T.refuse('MANIFEST', 'a manifest names at least one member or table property')
    end
    local function finite(n) return type(n) == 'number' and n == n and n ~= math.huge and n ~= -math.huge end
    local function score_refusal(id, v)
      local found = type(v)
      if v == nil then found = 'missing'
      elseif v == cjson.null then found = 'null'
      elseif found == 'number' then found = 'non-finite number' end
      return T.refuse('SCORE', id, found)
    end
    local function reserved(f) return f == 'epoch' or f == 'revision' or string.sub(f, 1, 6) == 'place:' end
    for idx, entry in ipairs(manifest.members) do
      if type(entry) ~= 'table' or not T.word(entry.id) then
        return T.refuse('MEMBER', 'entry ' .. idx .. ' is not an object with a nonempty id without control characters')
      end
      local id = entry.id
      local over = T.over('member_id_bytes', #id)
      if over then return over end
      if seen[id] then return T.refuse('TWICE', id) end
      seen[id] = true
      if entry.remove ~= nil and entry.remove ~= true then return T.refuse('ARGS', 'remove must be true', id) end

      if entry.set ~= nil then
        if type(entry.set) ~= 'table' then return T.refuse('ARGS', 'set must be object', id) end
        local count = 0
        for _ in pairs(entry.set) do count = count + 1 end
        over = T.over('set_fields', count, id)
        if over then return over end
        for f, val in pairs(entry.set) do
          if reserved(f) then return T.refuse('RESERVEDFIELD', id, T.excerpt(f)) end
          if not T.word(f) or type(val) ~= 'string' then
            return T.refuse('ARGS', 'field name and value must be valid strings', id)
          end
          over = T.over('field_value_bytes', #val, id)
          if over then return over end
        end
      end
      if entry.unset ~= nil then
        if type(entry.unset) ~= 'table' then return T.refuse('ARGS', 'unset must be array', id) end
        over = T.over('unset_fields', #entry.unset, id)
        if over then return over end
        local named = {}
        for _, f in ipairs(entry.unset) do
          if type(f) == 'string' and reserved(f) then return T.refuse('RESERVEDFIELD', id, T.excerpt(f)) end
          if not T.word(f) then return T.refuse('ARGS', 'invalid unset field name', id) end
          if named[f] then return T.refuse('ARGS', 'unset names a field twice', id) end
          named[f] = true
        end
      end
      if entry.set ~= nil and entry.unset ~= nil then
        for _, f in ipairs(entry.unset) do
          if entry.set[f] ~= nil then
            return T.refuse('MUTATION', id, 'field ' .. T.excerpt(f) .. ' cannot be both set and unset')
          end
        end
      end

      local exp = entry.expect
      if type(exp) ~= 'table' then return T.refuse('MANIFEST', 'member ' .. id .. ' missing expect record') end
      if exp.absent ~= nil then
        if exp.absent ~= true then return T.refuse('ARGS', 'expect absent must be true', id) end
        if exp.revision ~= nil or exp.place ~= nil or exp.fields ~= nil then
          return T.refuse('MUTATION', id, 'expect absent cannot combine with revision, place or fields')
        end
      end
      if exp.revision ~= nil and not T.uint(exp.revision) then
        return T.refuse('ARGS', 'expect revision must be a decimal string', id)
      end
      if exp.place ~= nil and (type(exp.place) ~= 'table' or type(exp.place.row) ~= 'string' or type(exp.place.col) ~= 'string') then
        return T.refuse('ARGS', 'expect place wants row and col strings', id)
      end
      if exp.fields ~= nil then
        if type(exp.fields) ~= 'table' then return T.refuse('ARGS', 'expect fields must be object', id) end
        local n = 0
        for _, guard in pairs(exp.fields) do
          n = n + 1
          if type(guard) == 'table' and type(guard.one_of) == 'table' then
            over = T.over('one_of_options', #guard.one_of, id)
            if over then return over end
          end
        end
        over = T.over('field_guards', n, id)
        if over then return over end
        for f, guard in pairs(exp.fields) do
          if type(guard) ~= 'table' then return T.refuse('FIELDGUARD', id, T.excerpt(f), 'guard must be object') end
          local conds = (guard.equals ~= nil and 1 or 0) + (guard.absent ~= nil and 1 or 0) + (guard.one_of ~= nil and 1 or 0)
          if conds ~= 1 then return T.refuse('FIELDGUARD', id, T.excerpt(f), 'exact one condition required') end
          if guard.equals ~= nil and type(guard.equals) ~= 'string' then return T.refuse('FIELDGUARD', id, T.excerpt(f), 'equals must be string') end
          if guard.absent ~= nil and guard.absent ~= true then return T.refuse('FIELDGUARD', id, T.excerpt(f), 'absent must be true') end
          if guard.one_of ~= nil then
            if type(guard.one_of) ~= 'table' or #guard.one_of == 0 then
              return T.refuse('FIELDGUARD', id, T.excerpt(f), 'one_of must be nonempty array')
            end
            local options = {}
            for _, opt in ipairs(guard.one_of) do
              if type(opt) ~= 'string' then return T.refuse('FIELDGUARD', id, T.excerpt(f), 'one_of items must be strings') end
              if options[opt] then return T.refuse('FIELDGUARD', id, T.excerpt(f), 'one_of names an option twice') end
              options[opt] = true
            end
          end
        end
      end

      if entry.create ~= nil then
        local cr = entry.create
        if type(cr) ~= 'table' or type(cr.row) ~= 'string' or type(cr.col) ~= 'string' then
          return T.refuse('ARGS', 'create wants row and col strings', id)
        end
        if not finite(cr.score) then return score_refusal(id, cr.score) end
        if entry.move ~= nil or entry.remove ~= nil then
          return T.refuse('MUTATION', id, 'create cannot combine with move or remove')
        end
        if exp.absent ~= true then return T.refuse('MUTATION', id, 'create requires expect absent') end
      end
      if entry.move ~= nil then
        local mv = entry.move
        if type(mv) ~= 'table' or type(mv.row) ~= 'string' or type(mv.col) ~= 'string' then
          return T.refuse('ARGS', 'move wants row and col strings', id)
        end
        if mv.score ~= nil and not finite(mv.score) then return score_refusal(id, mv.score) end
        if entry.remove ~= nil then return T.refuse('MUTATION', id, 'move cannot combine with remove') end
      end

      if entry.create ~= nil or entry.move ~= nil or entry.remove == true or
          (entry.set ~= nil and next(entry.set) ~= nil) or (entry.unset ~= nil and #entry.unset > 0) then
        changed = changed + 1
      else
        guards = guards + 1
      end
    end
    local over = T.over('changed_entries', changed)
    if over then return over end
    over = T.over('guard_entries', guards)
    if over then return over end
    return nil, changed, guards
  end

  function T.apply(keys, args)
    -- A refusal raised while one entry is judged carries that entry's id last.
    local function at_member(err, id) err[#err + 1] = id; return err end
    if #args ~= 2 then return T.refuse('ARGS', 'apply') end
    local table_name = args[1]
    local raw_json = args[2]
    if type(raw_json) ~= 'string' then return T.refuse('ARGS', 'apply payload') end
    local over = T.over('manifest_bytes', #raw_json)
    if over then return over end

    -- The operation is looked up first, before the request is judged: parse only
    -- as far as the table, the epoch and the operation id, and find the record. An
    -- identical recorded request returns its original result whatever rule a
    -- newer server applies to it; a different one under the same id is a conflict.
    local ops_key, op_field = T.opskey(table_name), nil
    local peek = T.decode(raw_json)
    if peek and peek.table == table_name and T.word(peek.operation_id) and T.uint(peek.epoch) then
      op_field = peek.epoch .. ':' .. peek.operation_id
      local recorded = redis.call('HGET', ops_key, op_field)
      if recorded then
        local record = cjson.decode(recorded)
        if record.request ~= raw_json then
          return T.refuse('OPCONFLICT', peek.operation_id)
        end
        -- the original result, and a third element saying it is a replay
        local original = cjson.decode(record.result)
        original[3] = 'REPLAY'
        return original
      end
    end

    -- Then the request is judged: the manifest's own rules, before the store is read.
    if not T.utf8(raw_json) then return T.refuse('MANIFEST', 'manifest is not valid UTF-8') end
    local manifest_err = T.validate_manifest_json(raw_json)
    if manifest_err then return manifest_err end
    local digest = redis.sha1hex(raw_json)
    local manifest = T.decode(raw_json)
    if not manifest or type(manifest) ~= 'table' then return T.refuse('MANIFEST', 'invalid json') end
    if manifest.schema ~= 1 then return T.refuse('SCHEMA', tostring(manifest.schema)) end
    if manifest.table ~= table_name then return T.refuse('ARGS', 'table mismatch') end
    if not T.word(manifest.operation_id) then return T.refuse('OPERATION', 'invalid operation_id') end
    if manifest.actor ~= nil and type(manifest.actor) ~= 'string' then return T.refuse('ARGS', 'actor must be a string') end
    if not T.uint(manifest.epoch) then return T.refuse('EPOCH', tostring(manifest.epoch)) end
    if not T.uint(manifest.expected_table_revision) then return T.refuse('REVISION', tostring(manifest.expected_table_revision)) end
    if type(manifest.members) ~= 'table' then return T.refuse('ARGS', 'members array required') end
    if not T.arrayfield(raw_json, 'members') then return T.refuse('MANIFEST', 'members must be json array') end
    local static_err, changed_count, guard_count = T.static_entries(manifest)
    if static_err then return static_err end
    op_field = manifest.epoch .. ':' .. manifest.operation_id

    -- Open the table exactly as every ordinary write does; the batch commits
    -- through T.finish, so the definition snapshot, the immutable identity,
    -- the template and the catalog are kept by the same code.
    local d, err = T.open(table_name)
    if not d then return err end
    if not d.present then return T.refuse('NOTABLE') end

    -- Unrecorded operation at another epoch refuses:
    if manifest.epoch ~= d.active then
      if T.uintgt(manifest.epoch, d.active) then
        return T.refuse('EPOCHAHEAD', manifest.epoch, d.active)
      end
      return T.refuse('STALE', manifest.epoch, d.active)
    end

    -- Expected table revision:
    if manifest.expected_table_revision ~= d.revision then
      return T.refuse('REVISION', manifest.expected_table_revision, d.revision)
    end

    -- The table's properties: every expectation holds before any write, and a
    -- write is a change only where the value differs (L1 contract amendment,
    -- table properties, section 4).
    local props_key = T.propskey(d)
    local props_now = T.hash(props_key)
    for name, value in pairs(manifest.prop_expect or {}) do
      if props_now[name] ~= value then return T.refuse('PROPGUARD', table_name, name) end
    end
    for _, name in ipairs(manifest.prop_absent or {}) do
      if props_now[name] ~= nil then return T.refuse('PROPGUARD', table_name, name) end
    end
    local props_changed, props_held = {}, 0
    for _ in pairs(props_now) do props_held = props_held + 1 end
    for name, value in pairs(manifest.props or {}) do
      if props_now[name] ~= value then
        if props_now[name] == nil then props_held = props_held + 1 end
        props_changed[name] = value
      end
    end
    local props_over = T.over('table_props', props_held)
    if props_over then return props_over end

    local members_list = manifest.members

    -- Pre-state evaluation & expectation checking:
    local member_records = {}
    local member_exists = {} -- the member has a record
    local member_cache = {} -- fields read by name: id -> field -> value, false for absent
    local function member_field(id, f)
      local cache = member_cache[id]
      if not cache then cache = {}; member_cache[id] = cache end
      local v = cache[f]
      if v == nil then
        v = redis.call('HGET', T.memberkey(d, id), f)
        cache[f] = v
      end
      if v == false then return nil end
      return v
    end
    local member_places = {}
    local member_scores = {}
    local member_score_text = {} -- the score exactly as the store holds it

    -- What the batch touches is counted from lengths before anything is read or
    -- hashed: the bytes of every before-value and after-value of the fields its
    -- entries name (set, unset and guarded), and the size the receipt must have at
    -- least. Either bound refuses the batch at once, before any read of a value.
    local touched, receipt_least = 0, 2
    for _, entry in ipairs(members_list) do
      local id = entry.id
      if type(id) == 'string' and T.word(id) then
        local mkey = T.memberkey(d, id)
        local counted = {}
        local function before_length(f)
          if counted[f] == nil then
            -- Only a record of the wrong type counts as empty here (it is refused
            -- below); any other error, a missing permission first, is raised.
            local n = redis.pcall('HSTRLEN', mkey, f)
            if type(n) == 'table' then
              if not (n.err and string.find(n.err, 'WRONGTYPE')) then T.rethrow(n) end
              n = 0
            end
            if n == 0 then
              local present = redis.pcall('HEXISTS', mkey, f)
              if type(present) == 'table' then
                if not (present.err and string.find(present.err, 'WRONGTYPE')) then T.rethrow(present) end
                present = 0
              end
              if present ~= 1 then n = false end
            end
            counted[f] = n
            touched = touched + (n or 0)
          end
          return counted[f]
        end
        local function side(key, n)
          if n == false then return #key + 7 end
          if n <= T.receipt_value_bytes then return #key + 5 + n end
          return #key + 7 + (#key + 9 + #tostring(n)) + (#key + 51)
        end
        for f, val in pairs(entry.set or {}) do
          local n = before_length(f)
          touched = touched + #val
          receipt_least = receipt_least + #f + 6 + side('before', n) + side('after', #val)
        end
        for _, f in ipairs(entry.unset or {}) do
          local n = before_length(f)
          receipt_least = receipt_least + #f + 9 + side('before', n) + side('after', false)
        end
        for f in pairs((entry.expect and entry.expect.fields) or {}) do before_length(f) end
        local over = T.over_least('batch_value_bytes', touched, id) or T.over_least('receipt_bytes', receipt_least, id)
        if over then return over end
      end
    end

    local idx
    local index_ids
    for _, entry in ipairs(members_list) do
      local id = entry.id
      local record, exists, why = T.member_head(d, id)
      if why then return why end
      member_records[id] = record or {}
      member_exists[id] = exists

      local current_place = record and record['place:' .. d.name]
      if current_place then
        local r, c = string.match(current_place, '^(.*):([^:]+)$')
        if not r or not c then return T.refuse('DRIFT', id, current_place) end
        local cell, cell_err = T.cell_once(d, r, c, true)
        if not cell then return at_member(cell_err, id) end
        local score = redis.pcall('ZSCORE', cell.key, id)
        if type(score) == 'table' and score.err then
          if string.find(score.err, 'WRONGTYPE') then return T.refuse('WRONGTYPE', cell.key, T.kind(cell.key), 'zset') end
          T.rethrow(score)
        end
        if not score then return T.refuse('DRIFT', r, c, id) end
        if not idx then
          index_ids = {}
          for _, e in ipairs(members_list) do index_ids[#index_ids + 1] = e.id end
          idx = T.place_index(d, index_ids)
        end
        local drift = T.index_drift(idx, id, current_place)
        if drift then return drift end
        member_places[id] = current_place
        member_scores[id] = tonumber(score)
        member_score_text[id] = score
      else
        if not idx then
          index_ids = {}
          for _, e in ipairs(members_list) do index_ids[#index_ids + 1] = e.id end
          idx = T.place_index(d, index_ids)
        end
        local drift = T.index_drift(idx, id, nil, 'set-only')
        if drift then return drift end
      end

      -- Check expectations (their shapes are settled by T.static_entries):
      local exp = entry.expect
      if exp.absent ~= nil then
        if exists or current_place then
          return T.refuse('MEMBEREXISTS', id, current_place and ('placed at ' .. current_place) or 'record without placement')
        end
      else
        if not exists then return T.refuse('NOTMEMBER', id, 'no member record', 'an existing member') end
        local obs_rev = record.revision or '0'
        if exp.revision and exp.revision ~= obs_rev then
          return T.refuse('MEMBERREVISION', id, exp.revision, obs_rev)
        end
        if exp.place then
          local exp_place = T.place(exp.place.row, exp.place.col)
          if current_place ~= exp_place then
            return T.refuse('PLACEGUARD', id, exp_place, current_place or 'unplaced')
          end
        end
        if exp.fields then
          for f, guard in pairs(exp.fields) do
            local actual = member_field(id, f)
            if guard.equals ~= nil then
              if actual == nil or actual ~= guard.equals then
                return T.refuse('FIELDGUARD', id, T.excerpt(f), 'equals', T.excerpt(guard.equals), T.excerpt(actual) or '<absent>')
              end
            elseif guard.absent ~= nil then
              if actual ~= nil then
                return T.refuse('FIELDGUARD', id, T.excerpt(f), 'absent', T.excerpt(actual))
              end
            elseif guard.one_of ~= nil then
              local matched = false
              if actual ~= nil then
                for _, opt in ipairs(guard.one_of) do
                  if actual == opt then matched = true; break end
                end
              end
              if not matched then
                return T.refuse('FIELDGUARD', id, T.excerpt(f), 'one_of', T.excerpt(cjson.encode(guard.one_of)), T.excerpt(actual) or '<absent>')
              end
            end
          end
        end
      end
    end

    -- Validate mutation constraints before staging:
    local plan = {}
    for _, entry in ipairs(members_list) do
      local id = entry.id
      local record = member_records[id]
      local current_place = member_places[id]
      local item = {id=id, entry=entry, record=record, current_place=current_place}

      local member_fields = {}
      local fields_changed = false
      if entry.set then
        for f, val in pairs(entry.set) do
          local bval = member_field(id, f)
          if bval ~= val then
            fields_changed = true
          end
          member_fields[f] = T.fieldchange(bval, val)
        end
      end
      if entry.unset then
        for _, f in ipairs(entry.unset) do
          local bval = member_field(id, f)
          if bval ~= nil then
            fields_changed = true
          end
          member_fields[f] = T.fieldchange(bval, nil)
        end
      end
      item.fields = member_fields

      if entry.create then
        if member_exists[id] or current_place then
          return T.refuse('MEMBEREXISTS', id, current_place and ('placed at ' .. current_place) or 'record without placement')
        end
        local crow, ccol = entry.create.row, entry.create.col
        local dst_cell, err = T.cell(d, crow, ccol, true)
        if not dst_cell then return at_member(err, id) end
        local score = entry.create.score
        item.action = 'create'
        item.effective_change = true
        item.dst_row = crow
        item.dst_col = ccol
        item.dst_place = T.place(crow, ccol)
        item.dst_cell = dst_cell
        item.score = score
        item.before_place = ''
        item.after_place = item.dst_place
        item.before_score = cjson.null
        item.after_score = nil -- the store's own string, read after the write
        item.before_rev = '0'
        item.after_rev = '1'
      elseif entry.move then
        if not current_place then return T.refuse('NOTMEMBER', id, member_exists[id] and 'record without placement' or 'no member record', 'a placed member to move') end
        local mrow, mcol = entry.move.row, entry.move.col
        local dst_cell, err = T.cell(d, mrow, mcol, true)
        if not dst_cell then return at_member(err, id) end
        local score
        if entry.move.score ~= nil then
          score = entry.move.score
        else
          score = member_scores[id]
        end
        local src_row, src_col = string.match(current_place, '^(.*):([^:]+)$')
        local src_cell, err2 = T.cell(d, src_row, src_col, true)
        if not src_cell then return at_member(err2, id) end
        local dst_place = T.place(mrow, mcol)
        local cur_score = member_scores[id]
        local place_changed = (dst_place ~= current_place)
        local score_changed = (score ~= cur_score)
        local effective = place_changed or score_changed or fields_changed

        item.src_row = src_row
        item.src_col = src_col
        item.src_place = current_place
        item.src_cell = src_cell
        item.dst_row = mrow
        item.dst_col = mcol
        item.dst_place = dst_place
        item.dst_cell = dst_cell
        item.score = score
        item.before_place = current_place
        item.after_place = dst_place
        item.before_score = member_score_text[id]
        item.after_score = (score == cur_score) and member_score_text[id] or nil
        item.before_rev = record.revision or '0'
        item.effective_change = effective

        if effective then
          item.action = 'move'
          item.after_rev = T.next(item.before_rev)
          if not item.after_rev then return T.refuse('OVERFLOW', id) end
        else
          item.action = 'noop'
          item.after_rev = item.before_rev
        end
      elseif entry.remove then
        if not current_place then return T.refuse('NOTMEMBER', id, member_exists[id] and 'record without placement' or 'no member record', 'a placed member to remove') end
        local src_row, src_col = string.match(current_place, '^(.*):([^:]+)$')
        local src_cell, err = T.cell(d, src_row, src_col, true)
        if not src_cell then return at_member(err, id) end
        item.action = 'remove'
        item.effective_change = true
        item.src_row = src_row
        item.src_col = src_col
        item.src_place = current_place
        item.src_cell = src_cell
        item.before_place = current_place
        item.after_place = ''
        item.before_score = member_score_text[id]
        item.after_score = cjson.null
        item.before_rev = record.revision or '0'
        item.after_rev = T.next(item.before_rev)
        if not item.after_rev then return T.refuse('OVERFLOW', id) end
      else
        item.before_place = current_place or ''
        item.after_place = current_place or ''
        item.before_score = member_score_text[id] ~= nil and member_score_text[id] or cjson.null
        item.after_score = member_score_text[id] ~= nil and member_score_text[id] or cjson.null
        item.before_rev = record.revision or '0'
        local has_fields = (entry.set ~= nil and next(entry.set) ~= nil) or
                           (entry.unset ~= nil and #entry.unset > 0)
        if has_fields then
          if not member_exists[id] then return T.refuse('NOTMEMBER', id, 'no member record', 'an existing member to change fields of') end
          if fields_changed then
            item.action = 'fields'
            item.effective_change = true
            item.after_rev = T.next(item.before_rev)
            if not item.after_rev then return T.refuse('OVERFLOW', id) end
          else
            item.action = 'noop'
            item.effective_change = false
            item.after_rev = item.before_rev
          end
        else
          item.action = 'guard'
          item.effective_change = false
          item.after_rev = item.before_rev
        end
      end

      plan[#plan + 1] = item
    end

    -- Stage mutations:
    local delta_members = {}
    local late = {} -- scores read back from the store after the writes
    local real_changes = 0

    for _, item in ipairs(plan) do
      local id = item.id
      local entry = item.entry
      local record = item.record or {}
      local mkey = T.memberkey(d, id)
      local delta_item = {
        id = id,
        before_place = item.before_place,
        after_place = item.after_place,
        before_score = item.before_score,
        -- never nil: the score the store holds is read back after the writes, and
        -- a key that comes and goes can change the order the encoder writes it in
        after_score = item.after_score == nil and '' or item.after_score,
        before_rev = item.before_rev,
        after_rev = item.after_rev,
        fields_set = T.smallvalues(entry.set),
        fields_unset = entry.unset or {},
        fields = item.fields,
      }
      delta_members[#delta_members + 1] = delta_item

      if item.effective_change then
        real_changes = real_changes + 1
      end

      if item.action == 'create' then
        T.stage(d, 'ZADD', item.dst_cell.key, item.score, id)
        T.stage(d, 'HSET', mkey, 'epoch', d.epoch, 'place:' .. d.name, item.dst_place, 'revision', '1')
        T.change(d, id, nil, item.dst_place, '')
        late[#late + 1] = {member = d.members[#d.members], delta = delta_item, key = item.dst_cell.key, id = id}
      elseif item.action == 'move' then
        if not record.epoch then
          T.stage(d, 'HSET', mkey, 'epoch', d.epoch)
        end
        if item.src_cell.key ~= item.dst_cell.key then
          T.stage(d, 'ZREM', item.src_cell.key, id)
          T.stage(d, 'ZADD', item.dst_cell.key, item.score, id)
          T.stage(d, 'HSET', mkey, 'place:' .. d.name, item.dst_place, 'revision', item.after_rev)
          T.change(d, id, item.src_place, item.dst_place, '')
        else
          T.stage(d, 'ZADD', item.dst_cell.key, item.score, id)
          T.stage(d, 'HSET', mkey, 'revision', item.after_rev)
          T.change(d, id, item.src_place, item.dst_place, '')
        end
        late[#late + 1] = {member = d.members[#d.members], delta = delta_item, key = item.dst_cell.key, id = id}
      elseif item.action == 'remove' then
        if not record.epoch then
          T.stage(d, 'HSET', mkey, 'epoch', d.epoch)
        end
        T.stage(d, 'ZREM', item.src_cell.key, id)
        T.stage(d, 'HDEL', mkey, 'place:' .. d.name)
        T.stage(d, 'HSET', mkey, 'revision', item.after_rev)
        T.change(d, id, item.src_place, nil, member_score_text[id])
      elseif item.action == 'fields' then
        if not record.epoch then
          T.stage(d, 'HSET', mkey, 'epoch', d.epoch)
        end
        T.stage(d, 'HSET', mkey, 'revision', item.after_rev)
      end

      if entry.set and next(entry.set) then
        local hcmd = {'HSET', mkey}
        for f, val in pairs(entry.set) do
          hcmd[#hcmd + 1] = f
          hcmd[#hcmd + 1] = val
        end
        T.stage(d, unpack(hcmd))
      end
      if entry.unset and #entry.unset > 0 then
        for i = 1, #entry.unset, 500 do
          local dcmd = {'HDEL', mkey}
          local last = math.min(i + 499, #entry.unset)
          for j = i, last do
            dcmd[#dcmd + 1] = entry.unset[j]
          end
          T.stage(d, unpack(dcmd))
        end
      end
    end

    local props_delta = {}
    if next(props_changed) then
      local hcmd = {'HSET', props_key}
      for name, value in pairs(props_changed) do
        hcmd[#hcmd + 1] = name
        hcmd[#hcmd + 1] = value
        props_delta[name] = value
      end
      T.stage(d, unpack(hcmd))
    end
    local outcome = (real_changes == 0 and not next(props_changed)) and 'noop' or 'changed'
    local encode_delta
    function encode_delta()
      return cjson.encode({
        operation_id = manifest.operation_id,
        digest = digest,
        actor = manifest.actor or '',
        selected_count = #manifest.members,
        guard_count = guard_count,
        changed_count = real_changes,
        members = delta_members,
        props = next(props_delta) and props_delta or nil,
      })
    end

    local receipt_over = T.over('receipt_bytes', T.receipt_size(encode_delta, late))
    if receipt_over then return receipt_over end

    -- The operation record is the last write, after the commit; its
    -- permission and type are settled before the first write.
    if not redis.acl_check_cmd('HSET', ops_key, op_field, '{}') then
      return T.refuse('NOPERM', 'HSET', ops_key)
    end
    local op_type_err = T.check_types({{'HSET', ops_key, op_field, '{}'}})
    if op_type_err then return op_type_err end

    return T.finish(d, 'apply', args, {
      actor = manifest.actor or '', fence = '', idem = '',
      outcome = outcome,
      wireargs = {table_name, manifest.operation_id, digest},
      event_extra = {'batch_delta', ''},
      -- Runs after every write but the event: a member's score is what the
      -- store holds, read back, so an event and a receipt carry the exact
      -- decimal string and two different scores never print the same.
      resolve = function()
        for _, l in ipairs(late) do
          local score = redis.call('ZSCORE', l.key, l.id)
          l.member.score = score
          l.delta.after_score = score
        end
        return encode_delta()
      end,
      record = function(reply, stream_id, before, after)
        redis.call('HSET', ops_key, op_field, cjson.encode({
          operation_id = manifest.operation_id,
          digest = digest,
          request = raw_json,
          stream_id = stream_id,
          epoch = d.epoch,
          rev_before = before,
          rev_after = after,
          outcome = outcome,
          result = cjson.encode(reply),
        }))
      end,
    }, {'OK'})
  end

  redis.register_function('ns_table_apply', function(keys, args)
    return T.apply(keys, args)
  end)
  redis.register_function{function_name = 'ns_table_read_set', flags = {'no-writes'}, callback = function(keys, args)
    return T.read_set(keys, args)
  end}
end

