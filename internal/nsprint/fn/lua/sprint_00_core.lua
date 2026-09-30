-- The sprint's one write path and one read path, core registry: the upper
-- design (EVENT-DRIVEN-TICK version 2.1) 1.0 and 8.0, as errata 1 E4 to E6
-- correct them; item IT12. A skeleton: no store loads it before gate G0
-- (Layer 1 revision 4 pinned, Layer 2 accepted again against its hash).
-- Like every tset fragment it runs only when the tset assembler set
-- NS.tset_profile; the legacy prelude never does, so in that library this
-- file defines nothing. Its Go twin is internal/sprint/sprintfn.
if NS.tset_profile then
NS.SP = {parts = {}, queries = {}, phases = {}}
do
  local SP = NS.SP
  -- 1.0: the phases of one step, in the one order ns_sprint_step runs them
  -- (sprintfn.PhaseOrder, held equal by a test).
  SP.phase_order = {'open', 'before', 'X.pre', 'derive', 'J', 'parts', 'plan', 'log', 'X.plan', 'prepare', 'commit'}
  -- 1.0: the parts a request may carry, in the order their pre and their
  -- commands run (sprintfn.PartOrder).
  SP.part_order = {'lease', 'pop', 'ingest', 'beat', 'clock', 'sprint'}
  -- The phases later items register: the before asks, X (IT13), derive (IT14)
  -- and J (IT15). A phase a step needs and no file registered refuses CONFIG.
  SP.phase_names = {before = true, x_pre = true, x_cmds = true, derive = true, j_decide = true, j_cmds = true}
  -- The query kinds of Layer 1 and Layer 2, which no sprint query may take (E6).
  SP.lower_kinds = {range = true, count = true, rcount = true, ids = true, rows = true, done = true,
    last = true, lines = true, cardlines = true}
  -- Fact F12: while the library loads, the only global is redis; ipairs, type,
  -- tostring, error and the rest exist only once a registered function runs.
  -- Everything this block and the registrations below run at load touches no
  -- other global: numeric loops over lists, nil tests in place of type.
  local known_part = {}
  for i = 1, #SP.part_order do known_part[SP.part_order[i]] = true end
  -- The registrations the load refused, in order (SP.sealed reads them).
  SP.refused = {}
  -- shown is a registration's name in a message, with no tostring: a string
  -- as it is, a number as "a name that is not a string"; a table or a boolean
  -- cannot be joined to a message, which raises and stops the load too.
  local function shown(name)
    if name == nil then return 'nil' end
    if (name .. '') == name then return name end
    return 'a name that is not a string'
  end
  -- A refused registration is kept in SP.refused and stops FUNCTION LOAD by
  -- reading a global no library defines: Redis refuses the load naming
  -- sprint_registration_refused (F12's nonexistent global), so no library
  -- with a doubled or a stray part, phase or query ever serves a call. Where
  -- globals read as nil (a plain Lua), the record stands, and every call
  -- refuses CONFIG (SP.sealed).
  local function refuse_registration(message)
    SP.refused[#SP.refused + 1] = message
    local stop = sprint_registration_refused
    return stop
  end
  -- A registration is checked when the library loads: a second writer of one
  -- name, a name 1.0 does not have, or a spec without its functions stops
  -- FUNCTION LOAD. That each is a function is checked by SP.sealed when the
  -- first call runs, since type is not a global of the load (F12).
  -- part:  spec.pre(ctx, req, obs)   -> plan, refusal   reads through S.readcmd; decides; writes nothing
  --        spec.cmds(ctx, plan, lp)  -> cmds, refusal   S.writecmd descriptors, A1 order; lp is Layer 2's log_plan
  function SP.part(name, spec)
    if name == nil or not known_part[name] or SP.parts[name] ~= nil or spec == nil or spec.pre == nil or spec.cmds == nil then
      return refuse_registration('sprint part ' .. shown(name) .. ': unknown, registered twice, or malformed')
    end
    SP.parts[name] = spec
  end
  -- query: spec.validate(q, index)   -> true, refusal   pure; before TIME and any store access
  --        spec.read(ctx, q, index)  -> answer, refusal through E6's helpers only; complete or refused
  function SP.query(name, spec)
    if name == nil or shown(name) ~= name or name == '' or SP.lower_kinds[name] or SP.queries[name] ~= nil or spec == nil or
        spec.validate == nil or spec.read == nil then
      return refuse_registration('sprint query ' .. shown(name) .. ': a lower kind, registered twice, or malformed')
    end
    SP.queries[name] = spec
  end
  -- phase: fn as 8.1 names it: before(ctx, sp) -> asks; x_pre(ctx, sp, obs) -> true, refusal;
  -- derive(ctx, intents, obs) -> entries, notes, refusal; j_decide(ctx, notes, obs) -> notes, jp, refusal;
  -- x_cmds(ctx, tp, lp) -> plan; j_cmds(ctx, jp, lp) -> plan (a plan is {commands = {...}})
  function SP.phase(name, fn)
    if name == nil or not SP.phase_names[name] or SP.phases[name] ~= nil or fn == nil then
      return refuse_registration('sprint phase ' .. shown(name) .. ': unknown, registered twice, or malformed')
    end
    SP.phases[name] = fn
  end
  -- sealed runs when a registered function runs, where type exists: the
  -- registry holds no refused registration and every spec's functions are
  -- functions. It returns the first problem, or nil, and remembers a clean
  -- registry so the check runs once per library.
  function SP.sealed()
    if SP.seal_ok then return nil end
    if #SP.refused > 0 then return SP.refused[1] end
    for name, spec in pairs(SP.parts) do
      if type(spec) ~= 'table' or type(spec.pre) ~= 'function' or type(spec.cmds) ~= 'function' then
        return 'sprint part ' .. name .. ': malformed'
      end
    end
    for name, spec in pairs(SP.queries) do
      if type(spec) ~= 'table' or type(spec.validate) ~= 'function' or type(spec.read) ~= 'function' then
        return 'sprint query ' .. tostring(name) .. ': malformed'
      end
    end
    for name, fn in pairs(SP.phases) do
      if type(fn) ~= 'function' then return 'sprint phase ' .. name .. ': malformed' end
    end
    SP.seal_ok = true
    return nil
  end
end
end
