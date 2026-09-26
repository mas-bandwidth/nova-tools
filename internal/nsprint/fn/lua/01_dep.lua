-- The one dependency rule (nova-tools #4318 follow-up, the land-duty round
-- #4373): when is a DEPENDS-ON edge on a task record met? Every reader of a
-- dependency calls this, never its own copy: the task queue's DEP.holds
-- (task_queue.lua), the release on a move (TK.move, 02_card_move.lua), and,
-- in Go, ws.DepMet (internal/nsprint/ws/dep.go), whose class test runs this
-- file's met_of against the same table (internal/nsprint/fn/dep_test.go).
--
--   a stream sentinel (<slug>:sentinel)  met only when landed: its done is a
--                                        rename's, never a landing
--   any other task id                    met when landed, or done with
--                                        where_ok not fail (a task done or
--                                        closed); a record that predates the
--                                        where field is met when its state is
--                                        landed, closed or done
--
-- A missing record is not met. This file loads before 02_card_move.lua and
-- reads nothing from NS; it exports NS.dep. The push-time refusals that keep
-- an edge from waiting for ever (a cycle, a sentinel with no record) are
-- TK.dep_refusal in 02_card_move.lua, which walks the edges with NS.dep.

local DP = {}

-- DP.is_sentinel: id is a stream sentinel's id (TK.is_sentinel says the same).
function DP.is_sentinel(id)
  return type(id) == 'string' and string.match(id, '^[a-z0-9][a-z0-9%-]*:sentinel$') ~= nil
end

-- DP.met_of(id, state, where, ok): the rule on a record's fields.
function DP.met_of(id, state, where, ok)
  state, where, ok = state or '', where or '', ok or ''
  if DP.is_sentinel(id) then
    return state == 'landed' or where == 'landed'
  end
  if where == 'landed' then return true end
  if where == 'done' then return ok ~= 'fail' end
  if where == '' then
    return state == 'landed' or state == 'closed' or state == 'done'
  end
  return false
end

-- DP.met(id): the rule on task:<id> as Redis holds it now.
function DP.met(id)
  local f = redis.call('HMGET', 'task:' .. id, 'state', 'where', 'where_ok')
  if not f[1] and not f[2] then return false end
  local function s(v)
    if v == false or v == nil then return '' end
    return v
  end
  return DP.met_of(id, s(f[1]), s(f[2]), s(f[3]))
end

-- The dependency classes (ws.DepClass in internal/nsprint/ws/dep.go, the
-- same table; fn's TestDepClassOneTable runs both): the one word every
-- reader of an edge prints for it.
--   met      the rule above says met
--   waiting  a live record, not met yet
--   parked   a record in parked
--   dead     a record in done that is not met (done/fail, a sentinel done
--            by hand)
--   unknown  no record has the id
--   cycle    the edge leads back to the task naming it (TK.dep_refusal
--            refuses it at the write doors, so no reader meets one)
DP.LEGACY = { closed = 'done', cancelled = 'done', done = 'done', open = 'ready', claimed = 'working' }

-- DP.class_of(id, state, where, ok): the class and the detail a reader
-- prints after it (the record's where, done with /where_ok; '' when the
-- class already says it).
function DP.class_of(id, state, where, ok)
  state, where, ok = state or '', where or '', ok or ''
  if state == '' and where == '' then return 'unknown', '' end
  local w = where
  if w == '' then w = DP.LEGACY[state] or state end
  local done = w == 'done'
  if done then
    if ok ~= '' then
      w = w .. '/' .. ok
    elseif state == 'cancelled' then
      w = w .. '/fail'
    end
  end
  local class = 'waiting'
  if DP.met_of(id, state, where, ok) then
    class = 'met'
  elseif done then
    class = 'dead'
  elseif w == 'parked' then
    class = 'parked'
  end
  if w == class then w = '' end
  return class, w
end

-- DP.class(id): the class and detail of task:<id> as Redis holds it now.
function DP.class(id)
  local f = redis.call('HMGET', 'task:' .. id, 'state', 'where', 'where_ok')
  local function s(v)
    if v == false or v == nil then return '' end
    return v
  end
  return DP.class_of(id, s(f[1]), s(f[2]), s(f[3]))
end

-- DP.text(class, detail): 'dead done/fail', 'waiting working', 'unknown'
-- (ws.DepText).
function DP.text(class, detail)
  if detail == nil or detail == '' then return class end
  return class .. ' ' .. detail
end

-- DP.ids(text): the task ids a DEPENDS-ON value names (a card's blocked_on
-- or a queue task's depends_on; entries split on ',', ';' and white space):
-- task:<id> names id, a bare id (a stream sentinel's among them) names
-- itself; owner/repo#n, key:, spec: and the other forms name no task.
function DP.ids(text)
  local out, seen = {}, {}
  for e in string.gmatch(text or '', '[^,;%s]+') do
    local id
    if string.sub(e, 1, 5) == 'task:' then
      id = string.sub(e, 6)
    elseif DP.is_sentinel(e) then
      id = e
    elseif e ~= 'none' and string.match(e, '^[A-Za-z0-9][A-Za-z0-9._-]*$') then
      id = e
    end
    if id and id ~= '' and not seen[id] then
      seen[id] = true
      out[#out + 1] = id
    end
  end
  return out
end

-- DP.LEAD: the first word ready --why prints for a class that blocks
-- (internal/nsprint/ready taskBlocker, the same table).
DP.LEAD = { waiting = 'WAIT', parked = 'WAIT', dead = 'DEAD', unknown = 'UNKNOWN', cycle = 'CYCLE' }

-- DP.blocker(entry): nil when the DEPENDS-ON entry names no task (key:,
-- owner/repo#n, ...) or its task edge is met now; else the line ready --why
-- prints for it, '<LEAD> <entry> <class text>' (WAIT task:alpha:sentinel
-- waiting). Every claim door calls this at the instant of the claim, in its
-- own FCALL (nova-tools #4414, Stella's reopened sentinel): an edge met once
-- and returned to unmet blocks again.
function DP.blocker(e)
  local id = DP.ids(e)[1]
  if not id then return nil end
  local class, detail = DP.class(id)
  if class == 'met' then return nil end
  return (DP.LEAD[class] or 'WAIT') .. ' ' .. e .. ' ' .. DP.text(class, detail)
end

-- DP.first_blocker(text): the first entry of a DEPENDS-ON value (split as
-- DP.ids splits) with a blocker, as (entry, line); nil when none has one.
function DP.first_blocker(text)
  for e in string.gmatch(text or '', '[^,;%s]+') do
    local line = DP.blocker(e)
    if line then return e, line end
  end
  return nil
end

NS.dep = { is_sentinel = DP.is_sentinel, met_of = DP.met_of, met = DP.met, ids = DP.ids,
  class_of = DP.class_of, class = DP.class, text = DP.text, blocker = DP.blocker,
  first_blocker = DP.first_blocker }
