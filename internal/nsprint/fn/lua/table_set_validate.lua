-- Static wire validation for tset/1. This fragment runs after table_set.lua.
-- It does not issue Redis commands: open must finish this phase before any IO.
if NS.tset_profile then
local S = NS.tset
local json
local function codec()
    if not json then
        json = cjson.new()
        json.decode_array_with_array_mt(true)
        json.decode_invalid_numbers(false)
        json.encode_invalid_numbers(false)
    end
    return json
end
-- FUNCTION LOAD evaluates this fragment before Redis exposes the runtime
-- libraries. Keep the codec access behind calls made by a registered function.
S.json = {
    encode = function(v) return codec().encode(v) end,
    decode = function(v) return codec().decode(v) end,
}

local U64_MAX = '18446744073709551615'
local DEFAULT = {
    request_bytes = 4194304, read_request_bytes = 4194304,
    entries = 256, queries = 1024, ids_per_entry = 2000,
    member_candidates = 2000, guard_members = 4000,
    rows = 100, advance_rows = 1024, name = 256,
    field_names = 128, field_value = 65536, result = 4096,
    intent = 65536, notes = 100, about = 4000,
    props = 64, prop_entries = 64,
}
local function cap(k)
    return S.limits and S.limits[k] or DEFAULT[k]
end
local function utf8_valid(s)
    if type(s) ~= 'string' then return false end
    local i, n = 1, #s
    while i <= n do
        local a = string.byte(s, i)
        if a < 128 then i = i + 1
        else
            local b, c, d = string.byte(s, i+1), string.byte(s, i+2), string.byte(s, i+3)
            local function cont(x) return x and x >= 128 and x <= 191 end
            if a >= 194 and a <= 223 and cont(b) then i = i + 2
            elseif a == 224 and b and b >= 160 and b <= 191 and cont(c) then i = i + 3
            elseif ((a >= 225 and a <= 236) or (a >= 238 and a <= 239)) and cont(b) and cont(c) then i = i + 3
            elseif a == 237 and b and b >= 128 and b <= 159 and cont(c) then i = i + 3
            elseif a == 240 and b and b >= 144 and b <= 191 and cont(c) and cont(d) then i = i + 4
            elseif a >= 241 and a <= 243 and cont(b) and cont(c) and cont(d) then i = i + 4
            elseif a == 244 and b and b >= 128 and b <= 143 and cont(c) and cont(d) then i = i + 4
            else return false end
        end
    end
    return true
end
S.utf8_valid = utf8_valid
-- CJSON turns JSON numbers into doubles. A fractional lexeme can round to a
-- whole number before uint_count sees it, so inspect number tokens in the raw
-- request first. Metadata is opaque JSON and retains its own numeric domain.
local function exact_integer_lexeme(token, ceiling)
    local mantissa, exponent = token, 0
    local at = token:find('[eE]')
    if at then
        mantissa = token:sub(1, at - 1)
        local part, sign = token:sub(at + 1), 1
        if part:sub(1,1) == '-' then sign, part = -1, part:sub(2)
        elseif part:sub(1,1) == '+' then part = part:sub(2) end
        if not part:match('^%d+$') then return false end
        for i = 1, #part do
            exponent = math.min(ceiling, exponent * 10 + string.byte(part,i) - 48)
        end
        exponent = exponent * sign
    end
    if mantissa:sub(1,1) == '-' then mantissa = mantissa:sub(2) end
    local dot = mantissa:find('.', 1, true)
    local fractional = dot and (#mantissa - dot) or 0
    local digits = dot and (mantissa:sub(1,dot-1) .. mantissa:sub(dot+1)) or mantissa
    if not digits:match('^%d+$') then return false end
    if not digits:find('[1-9]') then return true end
    local trailing = 0
    for i = #digits, 1, -1 do
        if string.byte(digits,i) ~= 48 then break end
        trailing = trailing + 1
    end
    return exponent - fractional + trailing >= 0
end
local function exact_request_numbers(raw)
    local stack, depth, i, n = {}, 0, 1, #raw
    local ceiling = n * 2 + 1
    local root_bad = false
    local function record_bad(bad)
        local top = stack[depth]
        if top and top.kind == 'object' then top.bad[top.key or ''] = bad
        elseif top then top.bad = top.bad or bad
        else root_bad = bad end
    end
    while i <= n do
        local ch = raw:sub(i,i)
        if ch == '"' then
            local first = i
            i = i + 1
            while i <= n do
                local c = raw:sub(i,i)
                if c == '\\' then i = i + 2
                elseif c == '"' then i = i + 1; break
                else i = i + 1 end
            end
            local next_i = i
            while next_i <= n and raw:sub(next_i,next_i):match('%s') do next_i = next_i + 1 end
            if raw:sub(next_i,next_i) == ':' and depth > 0 and stack[depth]
                and stack[depth].kind == 'object' then
                local key = raw:sub(first+1,i-2)
                if key:find('\\', 1, true) then
                    local ok, decoded = pcall(codec().decode, raw:sub(first,i-1))
                    if not ok then return false end
                    key = decoded
                end
                stack[depth].key = key
                -- JSON object decoders retain the last duplicate name. The
                -- precision guard must judge the same surviving value.
                stack[depth].bad[key] = false
            end
        elseif ch == '{' or ch == '[' then
            local parent = stack[depth]
            local opaque = parent and (parent.opaque or (parent.kind == 'object' and parent.key == 'meta')) or false
            depth = depth + 1
            if depth > 16 then return false end
            stack[depth] = {kind = ch == '{' and 'object' or 'array', opaque = opaque,
                bad = ch == '{' and {} or false}
            i = i + 1
        elseif ch == '}' or ch == ']' then
            local top = stack[depth]
            local bad = top and top.bad or false
            if top and top.kind == 'object' then
                bad = false
                for _, value in pairs(top.bad) do if value then bad = true; break end end
            end
            stack[depth] = nil
            depth = depth - 1
            record_bad(bad)
            i = i + 1
        elseif ch == '-' or ch:match('%d') then
            local first = i
            repeat
                i = i + 1
                ch = raw:sub(i,i)
            until i > n or ch == ',' or ch == '}' or ch == ']' or ch:match('%s')
            local top = stack[depth]
            if not (top and top.opaque) and not (top and top.kind == 'object' and top.key == 'meta')
                and not exact_integer_lexeme(raw:sub(first,i-1), ceiling) then record_bad(true) end
        else
            i = i + 1
        end
    end
    return not root_bad
end
local function failure(code, detail)
    return nil, S.refuse(code, detail or {})
end
local function array()
    -- Redis's cjson marks arrays on decoded tables; it does not export an
    -- array_mt value. Decode [] to obtain an appendable marked empty table.
    return codec().decode('[]')
end
S.array = array
S.is_array = function(v)
    if type(v) ~= 'table' then return false end
    local mt = getmetatable(v)
    return type(mt) == 'table' and mt.__is_cjson_array == true
end
S.is_object = function(v)
    return type(v) == 'table' and not S.is_array(v)
end

-- Epochs and revisions never pass through Lua's imprecise numeric domain.
function S.uint(s)
    return type(s) == 'string' and #s > 0 and #s <= 20
        and (s == '0' or s:match('^[1-9][0-9]*$') ~= nil)
        and (#s < 20 or s <= U64_MAX)
end
function S.cmp(a, b)
    if #a ~= #b then return #a < #b and -1 or 1 end
    if a == b then return 0 end
    return a < b and -1 or 1
end
function S.next(s)
    if not S.uint(s) or s == U64_MAX then return nil end
    local i, carry, digits = #s, 1, {}
    while i > 0 do
        local d = string.byte(s, i) - 48 + carry
        if d == 10 then d, carry = 0, 1 else carry = 0 end
        digits[i] = string.char(d + 48)
        i = i - 1
    end
    local out = table.concat(digits)
    return carry == 1 and '1' .. out or out
end

-- Parse a decimal lexeme before numeric conversion. Lua tonumber alone accepts
-- whitespace/hex and silently maps nonzero underflow to zero. Redis 8.10.2
-- accepts some nonzero underflows and stores zero; tset deliberately excludes
-- every nonzero-to-zero spelling as well as nondecimal/inf spellings.
local function decimal(s)
    if type(s) ~= 'string' or s == '' or s:find('%z') then return false end
    local body = s
    local c = body:sub(1, 1)
    if c == '+' or c == '-' then body = body:sub(2) end
    local mantissa, exp = body, nil
    local p = body:find('[eE]')
    if p then
        mantissa, exp = body:sub(1, p - 1), body:sub(p + 1)
        if not exp:match('^[+-]?%d+$') then return false end
    end
    local valid = mantissa:match('^%d+%.?%d*$') or mantissa:match('^%.%d+$')
    if not valid then return false end
    return true, mantissa, exp
end
function S.score(s)
    local valid, mantissa = decimal(s)
    if not valid then return false end
    local n = tonumber(s)
    if not n or n ~= n or n == math.huge or n == -math.huge then return false end
    if n == 0 and mantissa:find('[1-9]') then return false end
    return true
end

-- ZRANGE BYSCORE and ZCOUNT use zslParseRange, which permits a leading '('
-- and infinities, and does not apply string2d's underflow/overflow refusal.
-- The differential functional test compares this predicate with both commands.
local function range_number(s)
    if type(s) ~= 'string' or s:find('%z') then return false end
    -- Redis's fast_float_strtod returns zero with an end pointer at the NUL
    -- terminator for an empty bound, including the body of a bare "(".
    if s == '' then return true end
    local lower = s:lower()
    if lower:match('^[+-]?inf$') or lower:match('^[+-]?infinity$') then return true end
    local valid = decimal(s)
    if valid then return true end
    -- The target range parser falls back to C strtod for leading whitespace
    -- and hexadecimal floating syntax. Keep trailing whitespace invalid.
    local trimmed = s:match('^[ \t\n\r\f\v]+(.+)$')
    if trimmed then return range_number(trimmed) end
    local body = s
    if body:sub(1, 1) == '+' or body:sub(1, 1) == '-' then body = body:sub(2) end
    return body:match('^0[xX]%x+%.?%x*[pP][+-]?%d+$') ~= nil
        or body:match('^0[xX]%.%x+[pP][+-]?%d+$') ~= nil
        or body:match('^0[xX]%x+%.?%x*$') ~= nil
        or body:match('^0[xX]%.%x+$') ~= nil
end
function S.score_bound(s)
    if type(s) ~= 'string' then return false end
    if s:sub(1, 1) == '(' then return range_number(s:sub(2)) end
    return range_number(s)
end
S.bound = S.score_bound

local function name_shape(s, allow_empty)
    return type(s) == 'string' and (allow_empty or #s > 0) and utf8_valid(s)
end
local function name(s, allow_empty)
    return name_shape(s, allow_empty) and #s <= cap('name')
end
S.name = name
local function symbolic_shape(s)
    return name_shape(s) and s:match('^[A-Za-z0-9_][A-Za-z0-9_.-]*$') ~= nil
end
local function symbolic(s)
    return symbolic_shape(s) and #s <= cap('name')
end
S.symbolic = symbolic
local function row_shape(s)
    if not name_shape(s) or s:find('[%z\1-\31\127]') then return false end
    -- UTF-8 C1 controls U+0080..U+009F are C2 80..9F. UTF-8 validity was
    -- checked by name_shape(), so these are the only multibyte Cc code points.
    for i = 1, #s - 1 do
        local a, b = string.byte(s, i), string.byte(s, i + 1)
        if a == 194 and b >= 128 and b <= 159 then return false end
    end
    return true
end
local function row(s)
    return row_shape(s) and #s <= cap('name')
end
function S.cell(s)
    if type(s) ~= 'string' then return nil end
    local r, c = s:match('^(.*):([^:]*)$')
    if not row(r) or not symbolic(c) then return nil end
    return r, c
end
local function over_identifier(s, shape)
    return shape(s) and #s > cap('name')
end
local function long_reserved_field(s)
    return type(s) == 'string' and #s > cap('name') and s:sub(1,6) == 'place:'
end
local function reserved_in_array(v)
    if not S.is_array(v) then return false end
    for i = 1, #v do if long_reserved_field(v[i]) then return true end end
    return false
end
local function reserved_in_map(v)
    if not S.is_object(v) then return false end
    for key in pairs(v) do if long_reserved_field(key) then return true end end
    return false
end
local function long_reserved_application_field(req, operation)
    if not S.is_object(req) then return false end
    if operation == 'step' and S.is_array(req.entries) then
        for i = 1, #req.entries do
            local e = req.entries[i]
            if S.is_object(e) then
                if e.kind == 'create' or e.kind == 'move' or e.kind == 'remove' then
                    if reserved_in_map(e.set) or reserved_in_array(e.before_fields) then return true end
                    if S.is_array(e.each) then
                        for j = 1, #e.each do if reserved_in_map(e.each[j]) then return true end end
                    end
                    if (e.kind == 'move' or e.kind == 'remove') and reserved_in_array(e.unset) then return true end
                elseif e.kind == 'guard' and reserved_in_array(e.before_fields) then return true end
            end
        end
    elseif operation == 'read' and S.is_array(req.queries) then
        for i = 1, #req.queries do
            local q = req.queries[i]
            if S.is_object(q) then
                if (q.kind == 'range' or q.kind == 'ids' or q.kind == 'cardlines') and reserved_in_array(q.fields) then return true end
                if q.kind == 'cardlines' and S.is_object(q.cursor) and reserved_in_array(q.cursor.fields) then return true end
            end
        end
    end
    return false
end
local function over_cell_component(s)
    if type(s) ~= 'string' then return false end
    local r, c = s:match('^(.*):([^:]*)$')
    if not row_shape(r) or not symbolic_shape(c) then return false end
    return #r > cap('name') or #c > cap('name')
end
local function over_array_names(v, shape)
    if not S.is_array(v) then return false end
    for i = 1, #v do if over_identifier(v[i], shape) then return true end end
    return false
end
local function over_array_cells(v)
    if not S.is_array(v) then return false end
    for i = 1, #v do if over_cell_component(v[i]) then return true end end
    return false
end
local function over_field_keys(v)
    if not S.is_object(v) then return false end
    for key in pairs(v) do if over_identifier(key, name_shape) then return true end end
    return false
end
local function over_entry_names(e)
    if not S.is_object(e) then return false end
    local kind = e.kind
    if kind == 'advance' then return false end
    if kind ~= 'create' and kind ~= 'move' and kind ~= 'remove' and kind ~= 'guard'
        and kind ~= 'rows' and kind ~= 'rowset' and kind ~= 'count' and kind ~= 'rcount'
        and kind ~= 'prop' and kind ~= 'propguard' then return false end
    if over_identifier(e.t, symbolic_shape) then return true end
    if kind == 'prop' or kind == 'propguard' then return over_identifier(e.name, symbolic_shape) end
    if kind == 'rows' then return over_array_names(e.add, row_shape) or over_array_names(e.del, row_shape) end
    if kind == 'rowset' then
        if S.is_array(e.rows) then
            for i = 1, #e.rows do
                local item = e.rows[i]
                if S.is_object(item) and over_identifier(item.row, row_shape) then return true end
            end
        end
        return false
    end
    if kind == 'count' or kind == 'rcount' then return over_array_cells(e.cells) end
    if over_array_names(e.ids, name_shape) or over_array_names(e.before_fields, name_shape) then return true end
    if kind == 'create' or kind == 'move' or kind == 'remove' then
        if over_array_names(e.about, name_shape) or over_field_keys(e.set) then return true end
        if S.is_array(e.each) then
            for i = 1, #e.each do if over_field_keys(e.each[i]) then return true end end
        end
    end
    if kind == 'create' then return over_cell_component(e.to) end
    if over_cell_component(e.from) then return true end
    if kind == 'move' and over_cell_component(e.to) then return true end
    if kind == 'move' or kind == 'remove' then
        return over_array_names(e.unset, name_shape)
    end
    return false
end
local function over_query_names(q)
    if not S.is_object(q) then return false end
    local kind = q.kind
    if kind == 'range' then
        if q.key == nil and (over_identifier(q.t, symbolic_shape) or over_cell_component(q.cell)
            or over_array_names(q.fields, name_shape)) then return true end
    elseif kind == 'count' or kind == 'rcount' then
        if over_identifier(q.t, symbolic_shape) or over_array_cells(q.cells) then return true end
    elseif kind == 'ids' then
        if over_identifier(q.t, symbolic_shape) or over_array_names(q.ids, name_shape)
            or over_array_names(q.fields, name_shape) then return true end
    elseif kind == 'rows' then
        if over_identifier(q.t, symbolic_shape) then return true end
    elseif kind == 'props' then
        if over_identifier(q.t, symbolic_shape) or over_array_names(q.names, symbolic_shape) then return true end
    elseif kind == 'cardlines' then
        if over_array_names(q.abouts, name_shape) or over_array_names(q.fields, name_shape) then return true end
    end
    if kind == 'done' and S.is_array(q.ops) then
        for i = 1, #q.ops do
            local op = q.ops[i]
            if S.is_object(op) and over_identifier(op.op, name_shape) then return true end
        end
    end
    if kind == 'cardlines' and S.is_object(q.cursor) then
        if over_array_names(q.cursor.fields, name_shape) then return true end
        if S.is_array(q.cursor.positions) then
            for i = 1, #q.cursor.positions do
                local p = q.cursor.positions[i]
                if S.is_object(p) and over_identifier(p.about, name_shape) then return true end
            end
        end
    end
    return false
end
local function over_request_identifiers(req, operation)
    if not S.is_object(req) then return false end
    if over_identifier(req.space, name_shape) then return true end
    if operation == 'step' then
        if over_identifier(req.op, name_shape) then return true end
        if S.is_array(req.entries) then
            for i = 1, #req.entries do if over_entry_names(req.entries[i]) then return true end end
        end
        if S.is_array(req.notes) then
            for i = 1, #req.notes do
                local note = req.notes[i]
                if S.is_object(note) and over_array_names(note.about, name_shape) then return true end
            end
        end
    elseif operation == 'read' and S.is_array(req.queries) then
        for i = 1, #req.queries do if over_query_names(req.queries[i]) then return true end end
    end
    return false
end
local function uint_count(n, max, positive)
    return type(n) == 'number' and n == math.floor(n) and n >= (positive and 1 or 0) and n <= max
end
local function only(o, allowed)
    if not S.is_object(o) then return false end
    for k in pairs(o) do
        if type(k) ~= 'string' or not allowed[k] then return false end
    end
    return true
end
local function dense(a, max, min)
    if not S.is_array(a) then return false end
    local n = #a
    if n > max or n < (min or 0) then return false end
    for k in pairs(a) do
        if type(k) ~= 'number' or k < 1 or k > n or k ~= math.floor(k) then return false end
    end
    return true
end
local function strings(a, max, pred, min)
    if not dense(a, max, min) then return false end
    for i = 1, #a do if not pred(a[i]) then return false end end
    return true
end
local function fields(o)
    if not S.is_object(o) then return false end
    local n = 0
    for k, v in pairs(o) do
        n = n + 1
        if n > cap('field_names') or not name(k) or type(v) ~= 'string' or #v > cap('field_value') then
            return false
        end
    end
    return true
end
local function over_array(v, limit)
    return S.is_array(v) and #v > limit
end
local function over_fields(o)
    if not S.is_object(o) then return false end
    local count = 0
    for k, v in pairs(o) do
        count = count + 1
        if count > cap('field_names') or (type(k) == 'string' and #k > cap('name'))
            or (type(v) == 'string' and #v > cap('field_value')) then return true end
    end
    return false
end
local function reserved(f)
    return f == 'epoch' or f == 'revision' or f:sub(1, 6) == 'place:'
end
local function json_tree(v, depth)
    local typ = type(v)
    if typ == 'table' then
        if depth > 16 then return false end
        if S.is_array(v) then
            if not dense(v, 4194304) then return false end
            for i = 1, #v do if not json_tree(v[i], depth + 1) then return false end end
        else
            for k, child in pairs(v) do
                if not utf8_valid(k) or not json_tree(child, depth + 1) then return false end
            end
        end
        return true
    end
    return (typ == 'string' and utf8_valid(v)) or typ == 'boolean'
        or (typ == 'number' and v == v and v ~= math.huge and v ~= -math.huge)
        or v == cjson.null
end
local function aligned(e, key, pred)
    if e[key] == nil then return true end
    if not dense(e[key], cap('ids_per_entry')) or #e[key] ~= #e.ids then return false end
    for i = 1, #e[key] do if not pred(e[key][i]) then return false end end
    return true
end
local function entry(e, i)
    local detail = {entry_index = i - 1}
    if S.is_object(e) then detail.table = e.t end
    if not S.is_object(e) or type(e.kind) ~= 'string' then return failure('REQUEST', detail) end
    local kind = e.kind
    local allowed = {
        create={kind=true,t=true,to=true,ids=true,scores=true,set=true,each=true,before_fields=true,about=true,meta=true},
        move={kind=true,t=true,from=true,to=true,ids=true,scores=true,revs=true,set=true,each=true,unset=true,before_fields=true,about=true,meta=true},
        remove={kind=true,t=true,from=true,ids=true,revs=true,set=true,each=true,unset=true,before_fields=true,about=true,meta=true},
        guard={kind=true,t=true,from=true,ids=true,revs=true,before_fields=true},
        count={kind=true,t=true,cells=true,max=true},
        rcount={kind=true,t=true,cells=true,min=true,max=true,atleast=true,atmost=true},
        rows={kind=true,t=true,add=true,del=true},
        rowset={kind=true,t=true,rows=true},
        advance={kind=true,from=true},
        -- Amendment 2026-09-30 (property), section 2.
        prop={kind=true,t=true,name=true,value=true},
        propguard={kind=true,t=true,name=true,value=true},
    }
    if not only(e, allowed[kind] or {}) then return failure('REQUEST', detail) end
    for _, spec in ipairs({
        {'ids', cap('ids_per_entry')}, {'scores', cap('ids_per_entry')},
        {'revs', cap('ids_per_entry')}, {'each', cap('ids_per_entry')},
        {'about', cap('ids_per_entry')}, {'unset', cap('field_names')},
        {'before_fields', cap('field_names')}, {'cells', 20000},
        {'max', 20000}, {'add', cap('advance_rows')}, {'del', cap('advance_rows')},
        {'rows', cap('advance_rows')},
    }) do
        if over_array(e[spec[1]], spec[2]) then return failure('LIMIT', detail) end
    end
    if over_fields(e.set) then return failure('LIMIT', detail) end
    if S.is_array(e.each) then
        for j = 1, #e.each do if over_fields(e.each[j]) then return failure('LIMIT', detail) end end
    end
    if kind == 'advance' then
        if not S.uint(e.from) then return failure('REQUEST', detail) end
        return true
    end
    if not symbolic(e.t) then return failure('REQUEST', detail) end
    if kind == 'prop' or kind == 'propguard' then
        -- Amendment 2026-09-30 (property), sections 1 and 2: an identifier
        -- name; a value bounded as a field value, required by prop and
        -- optional for propguard (omitted means the property is absent).
        if type(e.value) == 'string' and #e.value > cap('field_value') then return failure('LIMIT', detail) end
        if not symbolic(e.name) or (e.value ~= nil and type(e.value) ~= 'string')
            or (kind == 'prop' and e.value == nil) then return failure('REQUEST', detail) end
        return true
    end
    if kind == 'rowset' then
        if not dense(e.rows, cap('advance_rows')) then return failure('REQUEST', detail) end
        local seen = {}
        for j = 1, #e.rows do
            local item = e.rows[j]
            if not only(item, {row=true,rank=true}) or not row(item.row)
                or not S.uint(item.rank) or S.cmp(item.rank, '9007199254740991') > 0
                or seen[item.row] then return failure('REQUEST', detail) end
            seen[item.row] = true
        end
        return true
    end
    if kind == 'rows' then
        if e.add == nil and e.del == nil then return failure('REQUEST', detail) end
        if e.add ~= nil and not strings(e.add, cap('advance_rows'), row) then return failure('REQUEST', detail) end
        if e.del ~= nil and not strings(e.del, cap('advance_rows'), row) then return failure('REQUEST', detail) end
        return true
    end
    if kind == 'count' or kind == 'rcount' then
        if not strings(e.cells, 20000, function(s) return S.cell(s) ~= nil end, 1) then return failure('REQUEST', detail) end
        if kind == 'count' then
            if not dense(e.max, 20000, 1) or #e.cells ~= #e.max then return failure('REQUEST', detail) end
            for j = 1, #e.max do if not uint_count(e.max[j], 9007199254740991) then return failure('REQUEST', detail) end end
        else
            if not S.score_bound(e.min) or not S.score_bound(e.max) then return failure('REQUEST', detail) end
            if e.atleast == nil and e.atmost == nil then return failure('REQUEST', detail) end
            if e.atleast ~= nil and not uint_count(e.atleast, 9007199254740991) then return failure('REQUEST', detail) end
            if e.atmost ~= nil and not uint_count(e.atmost, 9007199254740991) then return failure('REQUEST', detail) end
            if e.atleast and e.atmost and e.atleast > e.atmost then return failure('REQUEST', detail) end
        end
        return true
    end
    if not strings(e.ids, cap('ids_per_entry'), name, 1) then return failure('REQUEST', detail) end
    if kind == 'create' then
        if not S.cell(e.to) or not aligned(e, 'scores', S.score) or e.scores == nil then return failure('REQUEST', detail) end
    else
        if not S.cell(e.from) then return failure('REQUEST', detail) end
        if e.to ~= nil and not S.cell(e.to) then return failure('REQUEST', detail) end
        if not aligned(e, 'scores', S.score) then return failure('REQUEST', detail) end
    end
    if not aligned(e, 'revs', S.uint) or not aligned(e, 'about', name)
        or not aligned(e, 'each', fields) then return failure('REQUEST', detail) end
    if e.set ~= nil and not fields(e.set) then return failure('REQUEST', detail) end
    if e.unset ~= nil and not strings(e.unset, cap('field_names'), name) then return failure('REQUEST', detail) end
    if e.before_fields ~= nil and not strings(e.before_fields, cap('field_names'), name) then return failure('REQUEST', detail) end
    if e.meta ~= nil and not S.is_object(e.meta) then return failure('REQUEST', detail) end
    if e.set then for f in pairs(e.set) do if reserved(f) then return failure('FIELDNAME', detail) end end end
    if e.each then
        for j = 1, #e.each do
            for f in pairs(e.each[j]) do if reserved(f) then return failure('FIELDNAME', detail) end end
        end
    end
    if e.unset then
        for j = 1, #e.unset do
            local f = e.unset[j]
            if reserved(f) then return failure('FIELDNAME', detail) end
        end
    end
    if e.before_fields then for j = 1, #e.before_fields do if reserved(e.before_fields[j]) then return failure('FIELDNAME', detail) end end end
    if e.unset then
        local unset = {}
        for j = 1, #e.unset do unset[e.unset[j]] = true end
        for j = 1, #e.ids do
            local effective, count = {}, 0
            for f in pairs(e.set or {}) do effective[f] = true end
            if e.each then for f in pairs(e.each[j]) do effective[f] = true end end
            for f in pairs(effective) do
                count = count + 1
                if unset[f] then
                    detail.ids = {e.ids[j]}
                    return failure('FIELDOVERLAP', detail)
                end
            end
            if count > cap('field_names') then
                detail.ids = {e.ids[j]}
                return failure('LIMIT', detail)
            end
        end
    elseif e.set and e.each then
        for j = 1, #e.ids do
            local effective, count = {}, 0
            for f in pairs(e.set) do effective[f] = true end
            for f in pairs(e.each[j]) do effective[f] = true end
            for _ in pairs(effective) do count = count + 1 end
            if count > cap('field_names') then
                detail.ids = {e.ids[j]}
                return failure('LIMIT', detail)
            end
        end
    end
    return true
end

local function note(n)
    if not only(n, {line=true,about=true}) or not only(n.line, {kind=true,meta=true})
        or n.line.kind ~= 'note' or not S.is_object(n.line.meta)
        or not strings(n.about, cap('about'), name) then return false end
    return true
end
local function validate_step(req, allow_derived_notes)
    if S.is_object(req) and S.is_array(req.entries) and #req.entries > cap('entries') then return failure('LIMIT') end
    if S.is_object(req) and S.is_array(req.notes) and #req.notes > cap('notes') then return failure('LIMIT') end
    if S.is_object(req) then
        if (type(req.space) == 'string' and #req.space > cap('name'))
            or (type(req.op) == 'string' and #req.op > cap('name'))
            or (type(req.intent) == 'string' and #req.intent > cap('intent'))
            or (type(req.result) == 'string' and #req.result > cap('result')) then return failure('LIMIT') end
    end
    if not only(req, {epoch=true,space=true,op=true,intent=true,result=true,entries=true,notes=true,fence=true})
        or not S.uint(req.epoch) or not name(req.space)
        or (req.op == nil) ~= (req.intent == nil)
        or (req.op ~= nil and not name(req.op))
        or (req.intent ~= nil and (type(req.intent) ~= 'string' or #req.intent > cap('intent')))
        or (req.result ~= nil and (type(req.result) ~= 'string' or #req.result > cap('result')))
        or not dense(req.entries, cap('entries')) then return failure('REQUEST') end
    -- A fence is an explicit original-request identity settlement. Empty
    -- ordinary named steps remain ordinary steps when the flag is absent.
    if req.fence ~= nil and (req.fence ~= true or req.op == nil
        or #req.entries ~= 0 or (req.notes ~= nil and
            (not S.is_array(req.notes) or #req.notes ~= 0))
        or (req.result ~= nil and req.result ~= '')) then return failure('REQUEST') end
    local advances, candidates, guards, abouts, row_names, tables = 0, 0, 0, 0, {}, {}
    local props, prop_pairs = 0, {}
    local rowset_tables, rowset_prefix, nonrowset_seen = {}, 0, false
    for i = 1, #req.entries do
        local e, err = entry(req.entries[i], i)
        if not e then return nil, err end
        local x = req.entries[i]
        if x.t then tables[x.t] = true end
        if x.kind == 'rowset' then
            if nonrowset_seen or rowset_tables[x.t] then return failure('REQUEST', {entry_index=i-1}) end
            rowset_tables[x.t], rowset_prefix = true, rowset_prefix + 1
            for j = 1, #x.rows do row_names[x.t .. '\0' .. x.rows[j].row] = true end
        elseif x.kind == 'advance' then
            nonrowset_seen = true
            advances = advances + 1
            -- An advance always needs a stable receipt identity, including a
            -- generic advance with no rowset/restoration entries.
            if req.op == nil then return failure('REQUEST', {entry_index=i-1}) end
            if i ~= rowset_prefix + 1 or advances > 1 then return failure('REQUEST', {entry_index=i-1}) end
        elseif x.kind == 'rows' then
            nonrowset_seen = true
            for _, key in ipairs({'add','del'}) do
                if x[key] then for j = 1, #x[key] do row_names[x.t .. '\0' .. x[key][j]] = true end end
            end
        elseif x.kind == 'guard' then
            nonrowset_seen = true
            guards = guards + #x.ids
        elseif x.kind == 'create' or x.kind == 'move' or x.kind == 'remove' then
            nonrowset_seen = true
            candidates = candidates + #x.ids
            if x.about then abouts = abouts + #x.about end
        elseif x.kind == 'prop' or x.kind == 'propguard' then
            -- Amendment 2026-09-30 (property), section 2: TWICE for a second
            -- prop on one (t,name); a propguard beside it is legal.
            nonrowset_seen = true
            props = props + 1
            if x.kind == 'prop' then
                local pair = x.t .. '\0' .. x.name
                if prop_pairs[pair] then return failure('TWICE', {entry_index=i-1, table=x.t, name=x.name}) end
                prop_pairs[pair] = true
            end
        else
            nonrowset_seen = true
        end
    end
    if rowset_prefix > 0 and advances ~= 1 then return failure('REQUEST') end
    local table_count = 0
    for _ in pairs(tables) do table_count = table_count + 1 end
    if table_count > 4 or candidates > cap('member_candidates') or guards > cap('guard_members') or abouts > cap('about')
        or props > cap('prop_entries') then
        return failure('LIMIT')
    end
    local rows_count = 0
    for _ in pairs(row_names) do rows_count = rows_count + 1 end
    if rows_count > (advances > 0 and cap('advance_rows') or cap('rows')) then return failure('LIMIT') end
    if req.notes ~= nil then
        if not dense(req.notes, cap('notes')) then return failure('REQUEST') end
        if #req.notes > 0 and req.op == nil and not allow_derived_notes then return failure('REQUEST') end
        for i = 1, #req.notes do
            if S.is_object(req.notes[i]) and over_array(req.notes[i].about, cap('about')) then return failure('LIMIT') end
            if not note(req.notes[i]) then return failure('REQUEST') end
            local distinct, seen = 0, {}
            for j = 1, #req.notes[i].about do
                local about = req.notes[i].about[j]
                if not seen[about] then
                    seen[about] = true
                    distinct = distinct + 1
                    if distinct > 2000 then return failure('LIMIT') end
                end
            end
            abouts = abouts + #req.notes[i].about
        end
        if abouts > cap('about') then return failure('LIMIT') end
    end
    -- The composed profile's static rules (Layer 2's L.check_step): part of
    -- this static phase, before any guard (L1 8). The standalone profile does
    -- not load the log, so NS.tlog is absent there.
    if NS.tlog then
        local _, err = NS.tlog.check_step(req)
        if err then return nil, err end
    end
    return req, nil
end

local BUILTIN_KINDS = {range=true,count=true,rcount=true,ids=true,rows=true,
    done=true,last=true,lines=true,cardlines=true,props=true}
-- The extension is trusted Lua code, but its registry still has to be a
-- bounded, dense and collision-free list before any read context is opened.
-- Sprint's eight composite kinds are included, and additional bounded
-- Sprint-key kinds use this same seam.
function S.extension_kinds(extension)
    if extension == nil then return nil, nil end
    if not only(extension, {kinds=true,validate=true,read=true})
        or type(extension.validate) ~= 'function' or type(extension.read) ~= 'function'
        or type(extension.kinds) ~= 'table' then return failure('CONFIG') end
    local kinds, n, count = extension.kinds, #extension.kinds, 0
    if n < 1 or n > cap('queries') then return failure('CONFIG') end
    local registered = {}
    for index, kind in pairs(kinds) do
        count = count + 1
        if type(index) ~= 'number' or index ~= math.floor(index)
            or index < 1 or index > n or not symbolic(kind)
            or BUILTIN_KINDS[kind]
            or registered[kind] then return failure('CONFIG') end
        registered[kind] = true
    end
    if count ~= n then return failure('CONFIG') end
    return registered, nil
end

local function query(q, i, kinds, extension)
    local detail = {query_index=i-1}
    if not S.is_object(q) or type(q.kind) ~= 'string' then return failure('REQUEST', detail) end
    if kinds and kinds[q.kind] then
        local called, valid, refusal = pcall(extension.validate, q, i-1)
        if not called then return failure('CONFIG', detail) end
        if valid == true and refusal == nil then return true end
        if valid == nil and type(refusal) == 'table'
            and refusal.status == 'refused' and type(refusal.code) == 'string'
            and type(refusal.detail) == 'table' then
            if refusal.detail.query_index == nil then refusal.detail.query_index = i - 1 end
            return nil, refusal
        end
        return failure('CONFIG', detail)
    end
    if over_array(q.cells, 20000) or over_array(q.ids, 10000)
        or over_array(q.fields, cap('field_names')) or over_array(q.ops, 2000)
        or over_array(q.abouts, 2000)
        or (q.kind == 'props' and over_array(q.names, cap('props'))) then return failure('LIMIT', detail) end
    if q.kind == 'range' and type(q.limit) == 'number' and q.limit > 2000 then return failure('LIMIT', detail) end
    if q.kind == 'lines' and type(q.limit) == 'number' and q.limit > 5000 then return failure('LIMIT', detail) end
    if q.kind == 'lines' and type(q.ids_limit) == 'number' and q.ids_limit > 200000 then return failure('LIMIT', detail) end
    -- bytes_limit is bounded by the 8 MiB encoded reply (L1 7; decision 6).
    if q.kind == 'lines' and type(q.bytes_limit) == 'number' and q.bytes_limit > 8388608 then return failure('LIMIT', detail) end
    if q.kind == 'cardlines' and type(q.limit) == 'number' and q.limit > 500 then return failure('LIMIT', detail) end
    local common = {kind=true,t=true,cell=true,key=true,min=true,max=true,limit=true,desc=true,records=true,fields=true}
    if q.kind == 'range' then
        if not only(q, common) or (q.key == nil) == (q.t == nil or q.cell == nil)
            or (q.key ~= nil and (q.t ~= nil or q.cell ~= nil or q.records ~= nil or q.fields ~= nil))
            or (q.key ~= nil and (type(q.key) ~= 'string' or #q.key == 0))
            or (q.key == nil and (not symbolic(q.t) or not S.cell(q.cell)))
            or not S.score_bound(q.min) or not S.score_bound(q.max)
            or not uint_count(q.limit, 2000, true)
            or (q.desc ~= nil and type(q.desc) ~= 'boolean')
            or (q.records ~= nil and type(q.records) ~= 'boolean')
            or (q.fields ~= nil and (q.records ~= true or not strings(q.fields, cap('field_names'), name))) then return failure('REQUEST', detail) end
        if q.fields then for j = 1, #q.fields do if reserved(q.fields[j]) then return failure('FIELDNAME', detail) end end end
    elseif q.kind == 'count' or q.kind == 'rcount' then
        local allowed = {kind=true,t=true,cells=true}
        if q.kind == 'rcount' then allowed.min, allowed.max = true, true end
        if not only(q, allowed) or not symbolic(q.t)
            or not strings(q.cells, 20000, function(s) return S.cell(s) ~= nil end, 1)
            or (q.kind == 'rcount' and (not S.score_bound(q.min) or not S.score_bound(q.max))) then return failure('REQUEST', detail) end
    elseif q.kind == 'ids' then
        if not only(q, {kind=true,t=true,ids=true,fields=true}) or not symbolic(q.t)
            or not strings(q.ids, 10000, name, 1)
            or (q.fields ~= nil and not strings(q.fields, cap('field_names'), name)) then return failure('REQUEST', detail) end
        if q.fields then for j = 1, #q.fields do if reserved(q.fields[j]) then return failure('FIELDNAME', detail) end end end
    elseif q.kind == 'rows' then
        if not only(q, {kind=true,t=true}) or not symbolic(q.t) then return failure('REQUEST', detail) end
    elseif q.kind == 'props' then
        -- Amendment 2026-09-30 (property), section 3: optional distinct names.
        if not only(q, {kind=true,t=true,names=true}) or not symbolic(q.t)
            or (q.names ~= nil and not strings(q.names, cap('props'), symbolic)) then return failure('REQUEST', detail) end
        local seen = {}
        for j = 1, #(q.names or {}) do
            if seen[q.names[j]] then return failure('REQUEST', detail) end
            seen[q.names[j]] = true
        end
    elseif q.kind == 'done' then
        if not only(q, {kind=true,ops=true}) or not dense(q.ops, 2000, 1) then return failure('REQUEST', detail) end
        local seen = {}
        for j = 1, #q.ops do
            local op = q.ops[j]
            if not only(op,{epoch=true,op=true,intent_digest=true}) or not S.uint(op.epoch)
                or not name(op.op) or type(op.intent_digest) ~= 'string'
                or not op.intent_digest:match('^[0-9a-f]+$') or #op.intent_digest ~= 40 then return failure('REQUEST', detail) end
            seen[op.epoch] = seen[op.epoch] or {}
            seen[op.epoch][op.op] = seen[op.epoch][op.op] or {}
            if seen[op.epoch][op.op][op.intent_digest] then return failure('REQUEST', detail) end
            seen[op.epoch][op.op][op.intent_digest] = true
        end
    elseif q.kind == 'last' then
        if not only(q,{kind=true}) then return failure('REQUEST', detail) end
    elseif q.kind == 'lines' then
        if not only(q,{kind=true,after_seq=true,through_seq=true,limit=true,ids_limit=true,bytes_limit=true})
            or not S.uint(q.after_seq) or (q.through_seq ~= nil and not S.uint(q.through_seq))
            or not uint_count(q.limit,5000,true)
            or (q.ids_limit ~= nil and not uint_count(q.ids_limit,200000))
            or (q.bytes_limit ~= nil and not uint_count(q.bytes_limit,8388608,true)) then return failure('REQUEST', detail) end
    elseif q.kind == 'cardlines' then
        if not only(q,{kind=true,abouts=true,limit=true,fields=true,include_meta=true,cursor=true})
            or not strings(q.abouts,2000,name,1) or not uint_count(q.limit,500,true)
            or (q.fields ~= nil and not strings(q.fields,cap('field_names'),name))
            or (q.include_meta ~= nil and type(q.include_meta) ~= 'boolean') then return failure('REQUEST', detail) end
        if q.fields then for j = 1, #q.fields do if reserved(q.fields[j]) then return failure('FIELDNAME', detail) end end end
        if q.cursor ~= nil then
            local cur = q.cursor
            if S.is_object(cur) and (over_array(cur.fields,cap('field_names')) or over_array(cur.positions,2000)) then
                return failure('LIMIT', detail)
            end
            if not only(cur,{epoch=true,fields=true,include_meta=true,positions=true})
                or not S.uint(cur.epoch)
                or not strings(cur.fields,cap('field_names'),name)
                or type(cur.include_meta) ~= 'boolean'
                or not dense(cur.positions,2000,1) then return failure('REQUEST', detail) end
            for j = 1, #cur.fields do if reserved(cur.fields[j]) then return failure('FIELDNAME', detail) end end
            for j = 1, #cur.positions do
                local p = cur.positions[j]
                if S.is_object(p) and ((type(p.next_index) == 'number' and p.next_index > 9007199254740991)
                    or (type(p.through_index) == 'number' and p.through_index > 9007199254740991)
                    or (type(p.next_item) == 'number' and p.next_item > 9007199254740991)) then
                    return failure('OVERFLOW', detail)
                end
                -- A position is the pair (list index, item index within the
                -- line); next_item is present only when a page ended inside
                -- a line (L1 10, item 4; its name is Layer 2's revision 2).
                if not only(p,{about=true,next_index=true,through_index=true,next_item=true})
                    or not name(p.about)
                    or not uint_count(p.next_index,9007199254740991)
                    or (p.next_item ~= nil and not uint_count(p.next_item,9007199254740991))
                    or type(p.through_index) ~= 'number'
                    or p.through_index ~= math.floor(p.through_index)
                    or p.through_index < -1 or p.through_index > 9007199254740991 then return failure('REQUEST', detail) end
            end
        end
    else return failure('REQUEST', detail) end
    return true
end
S.validate_read_query = query
local function validate_read(req, extension, kinds)
    if S.is_object(req) and S.is_array(req.queries) and #req.queries > cap('queries') then return failure('LIMIT') end
    if S.is_object(req) and type(req.space) == 'string' and #req.space > cap('name') then return failure('LIMIT') end
    if not only(req,{epoch=true,space=true,mode=true,queries=true})
        or not S.uint(req.epoch) or not name(req.space)
        or (req.mode ~= nil and req.mode ~= 'atomic' and req.mode ~= 'page')
        or not dense(req.queries,cap('queries'),1) then return failure('REQUEST') end
    if req.mode == 'page' then
        local first = req.queries[1]
        if #req.queries ~= 1 or not S.is_object(first) or
            (first.kind ~= 'lines' and first.kind ~= 'cardlines') then return failure('REQUEST') end
    end
    for i = 1, #req.queries do
        local ok, err = query(req.queries[i],i,kinds,extension)
        if not ok then return nil, err end
        if req.queries[i].kind == 'cardlines' and req.queries[i].cursor ~= nil
            and req.mode ~= 'page' then return failure('REQUEST',{query_index=i-1}) end
    end
    return req, nil
end
local function validate_request(req, operation, extension, allow_derived_notes)
    local kinds
    if operation == 'read' then
        local err
        kinds, err = S.extension_kinds(extension)
        if err then return nil, err end
    elseif extension ~= nil then return failure('CONFIG') end
    if not json_tree(req,1) then return failure('REQUEST') end
    if long_reserved_application_field(req, operation) then return failure('FIELDNAME') end
    if over_request_identifiers(req, operation) then return failure('LIMIT') end
    if operation == 'step' then return validate_step(req, allow_derived_notes) end
    if operation == 'read' then return validate_read(req, extension, kinds) end
    return failure('REQUEST')
end
function S.validate_request(req, operation, extension)
    return validate_request(req, operation, extension, false)
end
-- Only the already-opened effective write phase may call this helper. The
-- original note count is captured before the trusted preplan can append to
-- the aliased notes array. Raw S.validate never reaches this exemption.
function S.validate_effective_step(req, original_note_count)
    if type(original_note_count) ~= 'number' or original_note_count < 0
        or original_note_count ~= math.floor(original_note_count)
        or original_note_count > cap('notes')
        or (S.is_object(req) and S.is_array(req.notes)
            and original_note_count > #req.notes) then return failure('REQUEST') end
    return validate_request(req, 'step', nil, original_note_count == 0)
end
function S.validate(version, raw, operation, extension)
    if version ~= 'tset/1' then return failure('VERSION') end
    if type(raw) ~= 'string' then return failure('REQUEST') end
    if #raw > (operation == 'read' and cap('read_request_bytes') or cap('request_bytes')) then return failure('LIMIT') end
    if not utf8_valid(raw) or not exact_request_numbers(raw) then return failure('REQUEST') end
    local ok, decoded = pcall(codec().decode, raw)
    if not ok then return failure('REQUEST') end
    -- Never forward an effective-step option from a raw caller request.
    if operation == 'read' then return S.validate_request(decoded, operation, extension) end
    return S.validate_request(decoded, operation)
end
end
