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
  -- A library's body runs at FUNCTION LOAD in an environment whose only global
  -- is redis: ipairs, pairs, type, error, tostring, math, table and string are
  -- all nonexistent there (TestFactF12LoadTimeSandboxHasNoSetmetatable;
  -- TestEveryProfileLoadsInTheLoadTimeEnvironment runs every profile that way).
  -- So nothing at the top level of a fragment, and nothing a registration
  -- runs, names a builtin: loops are indexed, and a registration is refused
  -- with no error() (see refuse), no type() and no tostring().
  local known_part = {}
  for i = 1, #SP.part_order do known_part[SP.part_order[i]] = true end
  -- refuse stops FUNCTION LOAD: there is no error() at load, so a refusal is an
  -- index of a field of SP that holds nothing, by a key that is the reason.
  -- The store's message names the field (sprint_part_refused, sprint_query_refused,
  -- sprint_phase_refused) and a Lua that names the key names the reason.
  local function refuse_part(name)
    return SP.sprint_part_refused['sprint part ' .. name .. ': unknown, registered twice, or malformed']
  end
  local function refuse_query(name)
    return SP.sprint_query_refused['sprint query ' .. name .. ': a lower kind, registered twice, or malformed']
  end
  local function refuse_phase(name)
    return SP.sprint_phase_refused['sprint phase ' .. name .. ': unknown, registered twice, or malformed']
  end
  -- A registration is checked when the library loads: a second writer of one
  -- name, or a name 1.0 does not have, or a spec with a field missing, stops
  -- FUNCTION LOAD, so no library with a doubled or a stray part, phase or query
  -- ever serves a call. What the fields hold (a function, a table) is a check
  -- that needs type(), so it is SP.shapes_ok's, which every call makes first
  -- and refuses CONFIG on.
  -- part:  spec.pre(ctx, req, obs)   -> plan, refusal   reads through S.readcmd; decides; writes nothing
  --        spec.cmds(ctx, plan, lp)  -> cmds, refusal   S.writecmd descriptors, A1 order; lp is Layer 2's log_plan
  function SP.part(name, spec)
    if name == nil or not known_part[name] or SP.parts[name] ~= nil or spec == nil or
        spec.pre == nil or spec.cmds == nil then
      return refuse_part(name)
    end
    SP.parts[name] = spec
  end
  -- query: spec.validate(q, index)   -> true, refusal   pure; before TIME and any store access
  --        spec.read(ctx, q, index)  -> answer, refusal through E6's helpers only; complete or refused
  function SP.query(name, spec)
    if name == nil or SP.lower_kinds[name] or SP.queries[name] ~= nil or spec == nil or
        spec.validate == nil or spec.read == nil then
      return refuse_query(name)
    end
    SP.queries[name] = spec
  end
  -- phase: fn as 8.1 names it: before(ctx, sp) -> asks; x_pre(ctx, sp, obs) -> true, refusal;
  -- derive(ctx, intents, obs) -> entries, notes, refusal; j_decide(ctx, notes, obs) -> notes, jp, refusal;
  -- x_cmds(ctx, tp, lp) -> plan; j_cmds(ctx, jp, lp) -> plan (a plan is {commands = {...}})
  function SP.phase(name, fn)
    if not SP.phase_names[name] or SP.phases[name] ~= nil or fn == nil then
      return refuse_phase(name)
    end
    SP.phases[name] = fn
  end
  -- shapes_ok: every registration holds what its kind needs (a part's spec is
  -- a table of two functions, a query's of two, a phase a function, a query's
  -- name a string). It runs in a call, where the builtins exist.
  function SP.shapes_ok()
    for _, spec in pairs(SP.parts) do
      if type(spec) ~= 'table' or type(spec.pre) ~= 'function' or type(spec.cmds) ~= 'function' then return false end
    end
    for name, spec in pairs(SP.queries) do
      if type(name) ~= 'string' or type(spec) ~= 'table' or type(spec.validate) ~= 'function' or
          type(spec.read) ~= 'function' then
        return false
      end
    end
    for _, fn in pairs(SP.phases) do
      if type(fn) ~= 'function' then return false end
    end
    return true
  end
end
end
