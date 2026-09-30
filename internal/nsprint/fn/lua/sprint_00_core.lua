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
  local known_part = {}
  for _, name in ipairs(SP.part_order) do known_part[name] = true end
  -- A registration is checked when the library loads: a second writer of one
  -- name, or a name 1.0 does not have, stops FUNCTION LOAD, so no library with
  -- a doubled or a stray part, phase or query ever serves a call.
  -- part:  spec.pre(ctx, req, obs)   -> plan, refusal   reads through S.readcmd; decides; writes nothing
  --        spec.cmds(ctx, plan, lp)  -> cmds, refusal   S.writecmd descriptors, A1 order; lp is Layer 2's log_plan
  function SP.part(name, spec)
    if not known_part[name] or SP.parts[name] ~= nil or type(spec) ~= 'table' or
        type(spec.pre) ~= 'function' or type(spec.cmds) ~= 'function' then
      error('sprint part ' .. tostring(name) .. ': unknown, registered twice, or malformed', 0)
    end
    SP.parts[name] = spec
  end
  -- query: spec.validate(q, index)   -> true, refusal   pure; before TIME and any store access
  --        spec.read(ctx, q, index)  -> answer, refusal through E6's helpers only; complete or refused
  function SP.query(name, spec)
    if type(name) ~= 'string' or SP.lower_kinds[name] or SP.queries[name] ~= nil or type(spec) ~= 'table' or
        type(spec.validate) ~= 'function' or type(spec.read) ~= 'function' then
      error('sprint query ' .. tostring(name) .. ': a lower kind, registered twice, or malformed', 0)
    end
    SP.queries[name] = spec
  end
  -- phase: fn as 8.1 names it: before(ctx, sp) -> asks; x_pre(ctx, sp, obs) -> true, refusal;
  -- derive(ctx, intents, obs) -> entries, notes, refusal; j_decide(ctx, notes, obs) -> notes, jp, refusal;
  -- x_cmds(ctx, tp, lp) -> plan; j_cmds(ctx, jp, lp) -> plan (a plan is {commands = {...}})
  function SP.phase(name, fn)
    if not SP.phase_names[name] or SP.phases[name] ~= nil or type(fn) ~= 'function' then
      error('sprint phase ' .. tostring(name) .. ': unknown, registered twice, or malformed', 0)
    end
    SP.phases[name] = fn
  end
end
end
