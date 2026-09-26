-- Typed lines as records (nova-tools #3595). No shebang: the loader
-- (internal/nsprint/fn/loader.go) prepends the single library header. One
-- do-block, so these locals never add to the shared chunk's local count.
--
-- ns_line_post is the one writer of a typed line (SCORE, HOLD, REPAIR, SPEC,
-- SPEC-WRITTEN, CLOSE, ...). In one call it measures the PR gates from
-- Redis, refuses a typed gate that disagrees with a measured one, and writes:
--   pr:<name>:<n>:line:<head>:<who>:<kind>  hash: repo n head who kind score
--       gates (measured where measured, else as typed) gates_typed measured
--       (the gate names measured) text source at; keyed by who, so a second
--       line of the same kind by the same reader at the same head replaces it
--   pr:<name>:<n>:line:<head>               zset: <who>:<kind> by at (ms), the
--       lines at one head oldest first (ns_line_list)
--   pr:<name>:<n>:lines                     list: every typed line, the per-PR log
--       the read brief reads
--   pr:<name>:<n>                           reads (first lines, newline-joined:
--       the stream lander's ReadAt), last_line, last_line_at
-- and, for a posted CLOSE or SCORE, the task move the line is an event for
-- (NS.tev.event, 03_task_event.lua, nova-tools #3779): the line and the move
-- together or neither. An import moves nothing.
-- Everything sits under the PR record's key, so the grant that lets a reader
-- post a line covers every key this writes.
--
-- The gates measured here, never typed: ci from ci:<name>:<head> field ci
-- (green ok, red red, pending pending; the record's ci when ci_sha is the
-- head), base from the record's base (dev, main or fixed-table-form ok, any
-- other branch no: stacked), scope from the caller's mirror diff against the
-- record's PATHS (ok|no, '' when unmeasured). A gate with no measure keeps
-- the typed word. A SCORE over 7 with a measured gate not ok is refused (the
-- rubric's gate cap). mode import records without refusing (a line already
-- posted as a comment is history, not a claim made now) and is idempotent on
-- source.
do
  local LN_BASES = { dev = true, main = true, ['fixed-table-form'] = true }
  local LN_MEASURED = { 'ci', 'base', 'scope' }
  local LN_CAP = 7

  local function ln_now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  -- ln_gates parses "ci:ok,base:ok,scope:no" into a map and the names in order.
  local function ln_gates(s)
    local m, order = {}, {}
    for part in string.gmatch(s or '', '[^,]+') do
      local k, v = string.match(part, '^%s*([%w_-]+):([%w_-]+)%s*$')
      if k and not m[k] then
        m[k] = v
        order[#order + 1] = k
      end
    end
    return m, order
  end

  local function ln_ok(v) return v == 'ok' end

  -- ln_head expands a typed head prefix to the record's full head.
  local function ln_head(head, rhead)
    if rhead ~= '' and #head < 40 and string.sub(rhead, 1, #head) == head then
      return rhead
    end
    return head
  end

  -- ns_line_post args: mode (post|import), repo (bare name), n, head, who,
  -- kind, score ('' or 0..10), gates as typed, scope (ok|no|''), scope why,
  -- text (the whole typed line), source ('' or comment:<id>).
  -- Reply: MISSING <record> | REFUSED <why> | EXISTS <line key> |
  -- POSTED <line key> <full head> <gates> <measured> <lines in the log>
  -- <tasks moved> <same> <skipped> <cut> <note>... (the notes 'id: why' per skip;
  -- cut is the copies the SCORE cut, #4094).
  local function line_post(keys, args)
    local mode, repo, n, head, who, kind = args[1] or '', args[2] or '', args[3] or '',
      string.lower(args[4] or ''), string.lower(args[5] or ''), args[6] or ''
    local score, typed, scope, scope_why = args[7] or '', args[8] or '', args[9] or '', args[10] or ''
    local text, source = args[11] or '', args[12] or ''
    if repo == '' or n == '' or head == '' or who == '' or kind == '' or text == '' then
      return redis.error_reply('ns_line_post: repo, n, head, who, kind and text are required')
    end
    local rk = 'pr:' .. repo .. ':' .. n
    local rec = redis.call('HMGET', rk, 'head', 'base', 'ci', 'ci_sha')
    local rhead = rec[1] or ''
    if rhead == '' then
      return { 'MISSING', rk }
    end
    head = ln_head(head, rhead)
    local m = {}
    local word = redis.call('HGET', 'ci:' .. repo .. ':' .. head, 'ci')
    if not word and rec[4] == head then word = rec[3] end
    if word == 'green' then m.ci = 'ok' elseif word == 'red' then m.ci = 'red' elseif word == 'pending' then m.ci = 'pending'
    elseif word and word ~= '' then
      -- any other CI word (error, cancelled, a typo) left ci unmeasured,
      -- and the typed gate and the score cap then went unchecked
      return { 'REFUSED', 'ci:' .. repo .. ':' .. string.sub(head, 1, 8) .. ' ci=' .. tostring(word) .. ' is not green, red or pending' }
    end
    local base = rec[2] or ''
    if base ~= '' then
      if LN_BASES[base] then m.base = 'ok' else m.base = 'no' end
    end
    if scope == 'ok' or scope == 'no' then m.scope = scope end
    local why = { ci = 'ci:' .. repo .. ':' .. string.sub(head, 1, 8) .. ' is ' .. (word or 'missing'),
      base = 'the PR base is ' .. base, scope = 'files outside PATHS: ' .. scope_why }
    local tmap, torder = ln_gates(typed)
    local out, measured = {}, {}
    for _, g in ipairs(LN_MEASURED) do
      local t, v = tmap[g], m[g]
      if v then
        measured[#measured + 1] = g
        if t and mode ~= 'import' and ln_ok(t) ~= ln_ok(v) then
          return { 'REFUSED', 'gate ' .. g .. ' typed ' .. t .. ' but measured ' .. v .. ' (' .. why[g] .. '); type the gate as measured' }
        end
        out[#out + 1] = g .. ':' .. v
      elseif t then
        out[#out + 1] = g .. ':' .. t
      end
    end
    for _, g in ipairs(torder) do
      if g ~= 'ci' and g ~= 'base' and g ~= 'scope' then out[#out + 1] = g .. ':' .. tmap[g] end
    end
    if mode ~= 'import' and kind == 'SCORE' and (tonumber(score) or 0) > LN_CAP then
      for _, g in ipairs(measured) do
        if not ln_ok(m[g]) then
          return { 'REFUSED', 'score ' .. score .. '/10 is over the gate cap ' .. LN_CAP .. ': ' .. g .. ' measured ' .. m[g] .. ' (' .. why[g] .. ')' }
        end
      end
    end
    local lk = rk .. ':line:' .. head .. ':' .. who .. ':' .. kind
    if source ~= '' and redis.call('HGET', lk, 'source') == source then
      return { 'EXISTS', lk }
    end
    local at = ln_now_ms()
    local gates, mnames = table.concat(out, ','), table.concat(measured, ',')
    redis.call('HSET', lk, 'repo', repo, 'n', n, 'head', head, 'who', who, 'kind', kind,
      'score', score, 'gates', gates, 'gates_typed', typed, 'measured', mnames,
      'text', text, 'source', source, 'at', tostring(at))
    redis.call('ZADD', rk .. ':line:' .. head, at, who .. ':' .. kind)
    local first = string.match(text, '^[^\n]*')
    local logged = mode == 'import' and redis.call('LPOS', rk .. ':lines', text)
    local count
    if logged then
      count = redis.call('LLEN', rk .. ':lines')
    else
      count = redis.call('RPUSH', rk .. ':lines', text)
      local old = redis.call('HGET', rk, 'reads') or ''
      local reads = first
      if old ~= '' then reads = old .. '\n' .. first end
      redis.call('HSET', rk, 'reads', reads)
    end
    redis.call('HSET', rk, 'last_line', first, 'last_line_at', tostring(math.floor(at / 1000)))
    local ev = { moved = 0, same = 0, skipped = 0, notes = {} }
    if mode ~= 'import' then ev = NS.tev.event(repo, n, rk, first) end
    local reply = { 'POSTED', lk, head, gates, mnames, tostring(count),
      tostring(ev.moved), tostring(ev.same), tostring(ev.skipped), tostring(ev.cut or 0) }
    for _, note in ipairs(ev.notes) do reply[#reply + 1] = note end
    return reply
  end

  -- ns_line_list args: repo, n, head ('' is the record's head; a prefix
  -- expands). Reply: MISSING <record> | OK <full head> then one flat
  -- field/value array per line at that head, oldest first. Read-only.
  local function line_list(keys, args)
    local repo, n, head = args[1] or '', args[2] or '', string.lower(args[3] or '')
    local rk = 'pr:' .. repo .. ':' .. n
    local rhead = redis.call('HGET', rk, 'head') or ''
    if head == '' then head = rhead end
    if head == '' then
      return { 'MISSING', rk }
    end
    head = ln_head(head, rhead)
    local reply = { 'OK', head }
    for _, member in ipairs(redis.call('ZRANGE', rk .. ':line:' .. head, 0, -1)) do
      reply[#reply + 1] = redis.call('HGETALL', rk .. ':line:' .. head .. ':' .. member)
    end
    return reply
  end

  redis.register_function('ns_line_post', line_post)
  redis.register_function{ function_name = 'ns_line_list', callback = line_list, flags = { 'no-writes' } }
end
