-- ns_sprint_step and ns_sprint_read: the sprint's one write function and one
-- read function (upper design version 2.1, 1.0; errata 1 E4 to E6; item
-- IT12). A skeleton: its phases call what the later items register in
-- NS.SP (sprint_00_core.lua), and no store loads it before gate G0. Layer 1's
-- NS.tset and Layer 2's NS.tlog are resolved when a call runs, since
-- table_set*.lua sorts after this file (8.0).
--
--   FCALL    ns_sprint_step 0 tset/1 <step> <sprint>
--   FCALL_RO ns_sprint_read 0 tset/1 <read plan>
--
-- <step> is Layer 1's own request, which S.open validates and replays
-- unchanged; <sprint> is the sprint's half (sprintfn/wire.go). A success is
-- {"reply": <Layer 1's prepared reply>, "parts": {<part>: <its decision>}};
-- a refusal is Layer 1's refusal object.
if NS.tset_profile then
do
  local SP = NS.SP
  SP.work_table = 'work'
  -- The sprint half's fields that are effects: a fence carries none (E5).
  local effects = {'intents', 'guards', 'notes', 'quarantine', 'done', 'requeue',
    'lease', 'pop', 'ingest', 'beat', 'clock', 'sprint'}
  local function has_effect(sp)
    for _, name in ipairs(effects) do if sp[name] ~= nil then return true end end
    return false
  end
  -- The static phase: the sprint half decoded and shaped before TIME or any
  -- store access. Both halves together count against the request bound, as
  -- sprintfn's clients count them (errata 2, item 1): Layer 1's step alone is
  -- bounded again by S.open.
  local function decode_sprint(S, step_raw, raw)
    if type(step_raw) ~= 'string' or type(raw) ~= 'string' then return nil, S.refuse('REQUEST') end
    if #step_raw + #raw > S.limits.request_bytes then
      return nil, S.refuse('LIMIT', {budget = 'request_bytes'})
    end
    local ok, sp = pcall(S.json.decode, raw)
    if not ok or type(sp) ~= 'table' or type(sp.meta) ~= 'table' then return nil, S.refuse('REQUEST') end
    -- The sprint part owns the quarantine's records (sprint_parts.lua): a body
    -- that quarantines a card the part does not carry would be lost, since a
    -- part runs only when its field is set.
    if SP.quarantine_carried and not SP.quarantine_carried(sp) then return nil, S.refuse('REQUEST') end
    return sp, nil
  end
  -- A phase the step needs and no file registered: CONFIG, before any write.
  local function needed(S, name)
    local fn = SP.phases[name]
    if not fn then return nil, S.refuse('CONFIG') end
    return fn, nil
  end
  local function envelope(reply, parts) return '{"reply":' .. reply .. ',"parts":' .. parts .. '}' end
  -- Layer 1 and Layer 2, resolved when a call runs.
  local function layers() return NS.tset, NS.tlog end

  -- before: S.before of every id the request's entries and intents name, and
  -- of every id a phase asks for, with the fields asked (AL1; L1 1.1).
  local function before(S, ctx, sp)
    local asks, order = {}, {}
    local function add(t, ids, fields)
      local a = asks[t]
      if not a then a = {ids = {}, seen = {}, fields = {}, fseen = {}}; asks[t] = a; order[#order + 1] = t end
      for _, id in ipairs(ids or {}) do
        if not a.seen[id] then a.seen[id] = true; a.ids[#a.ids + 1] = id end
      end
      for _, f in ipairs(fields or {}) do
        if not a.fseen[f] then a.fseen[f] = true; a.fields[#a.fields + 1] = f end
      end
    end
    for _, e in ipairs(ctx.request.entries) do
      if e.kind == 'create' or e.kind == 'move' or e.kind == 'remove' or e.kind == 'guard' then
        add(e.t, e.ids, e.before_fields)
      end
    end
    for _, it in ipairs(sp.intents or {}) do
      -- 8.0 gives a needmet and a needgone a need and its waiters and no card:
      -- the empty card is not an id to read (S.before refuses it REQUEST), and
      -- the need, read below, is the card they are about (IT14).
      if not ((it.kind == 'needmet' or it.kind == 'needgone') and (it.card == nil or it.card == '')) then
        add(SP.work_table, {it.card}, nil)
      end
      add(SP.work_table, it.needs, nil)
      if it.need then add(SP.work_table, {it.need}, nil) end
      add(SP.work_table, it.waiters, nil)
    end
    if SP.phases.before then
      for _, a in ipairs(SP.phases.before(ctx, sp)) do add(a.table, a.ids, a.fields) end
    end
    table.sort(order)
    local obs = {}
    for _, t in ipairs(order) do
      local got, err = S.before(ctx, t, asks[t].ids, asks[t].fields)
      if err then return nil, err end
      obs[t] = got
    end
    return obs, nil
  end

  -- One step, in 1.0's order: open, before, X.pre, derive, J, parts, plan,
  -- log, X.plan, prepare, commit. Nothing before commit writes.
  local function step(keys, args)
    local S, L = layers()
    if #keys ~= 0 or #args ~= 3 then return S.json.encode(S.refuse('ARGS')) end
    -- A registry the load could not check whole (F12) is checked on the first
    -- call: a malformed one serves nothing (sprint_00_core.lua SP.sealed).
    local broken = SP.sealed()
    if broken then return S.json.encode(S.refuse('CONFIG', {}, broken)) end
    local sp, err = decode_sprint(S, args[2], args[3])
    if err then return S.json.encode(err) end
    -- open: Layer 1's size, version, definitions and receipt replay.
    local ctx
    ctx, err = S.open(args[1], args[2])
    if err then return S.json.encode(err) end
    -- E5: a fence carries no sprint effect. S.open has read TIME and the
    -- receipt by now and written nothing.
    if ctx.original_fence and has_effect(sp) then return S.json.encode(S.refuse('REQUEST')) end
    if ctx.replay then return envelope(S.json.encode(ctx.replay), '{}') end
    if ctx.original_fence then
      local commit
      commit, err = S.fence_prepare(ctx)
      if err then return S.json.encode(err) end
      return envelope(S.commit(commit), '{}')
    end
    -- The pre stage (AL1): each phase reads and decides; nothing writes.
    local obs
    obs, err = before(S, ctx, sp)
    if err then return S.json.encode(err) end
    local x_pre
    x_pre, err = needed(S, 'x_pre')
    if err then return S.json.encode(err) end
    local _, xerr = x_pre(ctx, sp, obs)
    if xerr then return S.json.encode(xerr) end
    local note_reqs = {}
    for _, n in ipairs(sp.notes or {}) do note_reqs[#note_reqs + 1] = n end
    if sp.intents and #sp.intents > 0 then
      local derive
      derive, err = needed(S, 'derive')
      if err then return S.json.encode(err) end
      local entries, notes, derr = derive(ctx, sp.intents, obs)
      if derr then return S.json.encode(derr) end
      for _, e in ipairs(entries or {}) do ctx.request.entries[#ctx.request.entries + 1] = e end
      for _, n in ipairs(notes or {}) do note_reqs[#note_reqs + 1] = n end
    end
    -- J runs for a step that carries a note request, and, once registered, for
    -- any step that carries entries: it closes the lateness judgment of each
    -- timed state they end (1.3.4), which no note request asks for.
    local jp
    if #note_reqs > 0 or (SP.phases.j_decide and #ctx.request.entries > 0) then
      local j_decide
      j_decide, err = needed(S, 'j_decide')
      if err then return S.json.encode(err) end
      local notes, plan, jerr = j_decide(ctx, note_reqs, obs)
      if jerr then return S.json.encode(jerr) end
      -- ctx.notes is the one staged notes array (L1 1.1): append, never replace.
      for _, n in ipairs(notes or {}) do ctx.notes[#ctx.notes + 1] = n end
      jp = plan
    end
    local plans, results = {}, {}
    for _, name in ipairs(SP.part_order) do
      if sp[name] ~= nil then
        local part = SP.parts[name]
        if not part then return S.json.encode(S.refuse('CONFIG')) end
        local plan, perr = part.pre(ctx, sp, obs)
        if perr then return S.json.encode(perr) end
        plans[#plans + 1] = {part = part, plan = plan}
        results[name] = plan
      end
    end
    -- plan and log: Layer 1's table plan, Layer 2's lines and seqs.
    local tp
    tp, err = S.plan(ctx)
    if err then return S.json.encode(err) end
    local lp
    lp, err = L.plan(ctx, tp)
    if err then return S.json.encode(err) end
    -- X.plan: commands only, in A1 order: X's, then J's, then the parts'.
    local x_cmds
    x_cmds, err = needed(S, 'x_cmds')
    if err then return S.json.encode(err) end
    local others = {x_cmds(ctx, tp, lp)}
    -- X.plan could not derive its commands: refused before prepare, saying why
    -- (nothing has been written)
    if ctx.x_poisoned then return S.json.encode(S.refuse('REQUEST', {}, 'X.plan: ' .. ctx.x_poisoned)) end
    -- Commands the derive phase staged for x_cmds to append (SP.intent_commands)
    -- that it left behind are refused, never dropped: open would be lowered with
    -- the card still in wait:<n> (I2). Nothing has been written yet (IT14).
    if SP.intent_commands_pending and SP.intent_commands_pending(ctx) then
      return S.json.encode(S.refuse('CONFIG', {}, 'the derive phase staged commands on wait:<n> or missing that X.plan did not take'))
    end
    if jp then
      local j_cmds
      j_cmds, err = needed(S, 'j_cmds')
      if err then return S.json.encode(err) end
      others[#others + 1] = j_cmds(ctx, jp, lp)
    end
    local parts_plan = {commands = {}}
    for _, p in ipairs(plans) do
      local cmds, cerr = p.part.cmds(ctx, p.plan, lp)
      if cerr then return S.json.encode(cerr) end
      for _, c in ipairs(cmds or {}) do parts_plan.commands[#parts_plan.commands + 1] = c end
    end
    others[#others + 1] = parts_plan
    -- The parts' reply is encoded before prepare, so nothing after the commit
    -- can fail.
    local parts_reply = '{}'
    if next(results) then
      local ok, encoded = pcall(S.json.encode, results)
      if not ok then return S.json.encode(S.refuse('REQUEST')) end
      parts_reply = encoded
    end
    -- prepare validates the whole list, the receipt last; commit runs it.
    local commit
    commit, err = S.prepare(ctx, tp, lp, others)
    if err then return S.json.encode(err) end
    return envelope(S.commit(commit), parts_reply)
  end

  -- One atomic read through Layer 1's enclosing read hook (AL5; E6): the
  -- sprint's kinds, from NS.SP.queries in sorted order, dispatched on q.kind.
  local function extension()
    local kinds = {}
    for name in pairs(SP.queries) do kinds[#kinds + 1] = name end
    table.sort(kinds)
    return {kinds = kinds,
      validate = function(q, index) return SP.queries[q.kind].validate(q, index) end,
      read = function(ctx, q, index) return SP.queries[q.kind].read(ctx, q, index) end}
  end
  -- Layer 1's frozen lifecycle: a write entry wraps its whole body once in
  -- S.run_context, which clears the call's context on the way in and out; a
  -- read entry calls S.read directly, which wraps itself, so a read wrapped
  -- again would be refused CONFIG. A wrong argument count on the read is
  -- refused inside a context of its own, as Layer 1's own read does.
  local function bad_args(S) return S.json.encode(S.refuse('ARGS')) end
  local function bad_registry(S, broken) return S.json.encode(S.refuse('CONFIG', {}, broken)) end
  local function read(keys, args)
    local S, L = layers()
    if #keys ~= 0 or #args ~= 2 then return S.run_context(bad_args, S) end
    local broken = SP.sealed()
    if broken then return S.run_context(bad_registry, S, broken) end
    return S.read(args[1], args[2], L and L.read or nil, extension())
  end

  redis.register_function('ns_sprint_step', function(keys, args)
    local S = layers()
    return S.run_context(step, keys, args)
  end)
  redis.register_function{function_name = 'ns_sprint_read', callback = read, flags = {'no-writes'}}
end
end
