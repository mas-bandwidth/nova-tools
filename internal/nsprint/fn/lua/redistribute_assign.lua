-- assign --stdin and redistribute --from (nova-tools #3103, spec #2756 v6
-- 4.6, control 46). No shebang: loader.go prepends the single library
-- header. loader.go wraps this file in its own do-block; it sorts after
-- friend.lua and redistribute.lua and imports their helpers (routing, the
-- reader pick, the transfer dedup of 5.7 (6)) from NS.friend and
-- NS.redistribute just below this comment.
--
-- ns_assign_batch moves many tasks in ONE call: args = S, reason, actor,
-- idem, then (task, friend) pairs. Every line is refused or moved by the same
-- rules a single assign follows (a batch of one IS the single assign), and
-- every line writes exactly one `task assign` receipt, so a batch of n lines
-- leaves the receipts of n single assigns. The reply is four values per
-- line: task, friend, status, detail.
--
--   MOVED     open task placed on friend's queue (detail from=<queue>)
--   SAME      already on friend's queue; nothing moves
--   NOTFOUND  no such task in S
--   UNKNOWN   friend is not registered
--   DOWN      friend has no beat, is paused or carries a state
--   LIVE      the task is claimed or working; moving it needs a fence
--             (assign --revoke, #2940), never a batch move
--   CLOSED    the task is closed, cancelled or reconcile-required
--   AUTHOR    a read or review of the friend's own PR
--   DEDUP     the friend already holds the read identity (repo, PR, full
--             head) or posted a typed line at that head (spec 5.7 (6))
--
-- ns_redistribute_from is a hand redistribution for one
-- friend f: args = f, reason, to ('' = route by kind), kinds (csv, '' = all),
-- actor, idem. The role roster is read from Redis in this call. The same
-- rd_route the tick uses moves each open task; every moved title carries
-- `[moved from <f>: <reason>]`. f's leases are closed only when f is out of
-- credits or has no beat (the tick's rule), never for an up friend. A down
-- f (friend:<f>:down, or no beat) also has its card queue emptied (#4145):
-- every card in friend:<f>:cards:ready and every lapsed lease in
-- friend:<f>:cards:working moves by NS.deal_friend.rebalance; when that
-- ready set is not empty and nothing moved the reply is { 'REFUSED',
-- reason }, never a silent moved=0. With
-- --to, a task the named friend cannot receive stays on f (KEPT). Reply:
-- { 'OK', { f, state, moved, leases, released, unrouted, kept }, events }
-- where events are four values each: MOVED id to marker, DEDUP id friend
-- reason, KEPT id f why.

local fs_clear = NS.friend.fs_clear
local RD = NS.redistribute
local RD_OUT = RD.RD_OUT
local FR = NS.friend_roles
local DF = NS.deal_friend
local fr_actor, fr_has_role, fr_roster = FR.fr_actor, FR.fr_has_role, FR.fr_roster
local rd_author, rd_caplog, rd_close_leases, rd_csv = RD.rd_author, RD.rd_caplog, RD.rd_close_leases, RD.rd_csv
local rd_dedup, rd_free, rd_log, rd_mark = RD.rd_dedup, RD.rd_free, RD.rd_log, RD.rd_mark
local rd_move_open, rd_note_held, rd_now_ms, rd_open_sprints = RD.rd_move_open, RD.rd_note_held, RD.rd_now_ms, RD.rd_open_sprints

local function ra_wake(woken, at, actor, idem, why)
  local targets = {}
  for g in pairs(woken) do targets[#targets + 1] = g end
  table.sort(targets)
  for _, g in ipairs(targets) do
    local wake = 'friend:' .. g .. ':wake'
    redis.call('LPUSH', wake, tostring(at) .. ':' .. why)
    redis.call('LTRIM', wake, 0, 0)
    rd_caplog('friend-wake', g, why, actor, idem, at)
  end
end

-- ra_queue_of names where open task id sits: 'ready', a friend, or nil.
local function ra_queue_of(S, id, friends)
  local ready = redis.call('ZSCORE', 's:' .. S .. ':ready', id)
  if ready then
    return 'ready', ready
  end
  for _, g in ipairs(friends) do
    local score = redis.call('ZSCORE', 's:' .. S .. ':open:' .. g, id)
    if score then
      return g, score
    end
  end
  return nil, nil
end

-- ra_assign_one applies the single-assign rules to one line and writes its
-- one receipt. ctx carries the per-call caches.
local function ra_assign_one(ctx, id, g)
  local S = ctx.S
  local key = 'task:' .. id
  local state = redis.call('HGET', key, 'state')
  local kind = redis.call('HGET', key, 'kind') or 'work'
  local status, detail = nil, ''
  if not state then
    status = 'NOTFOUND'
  elseif redis.call('SISMEMBER', 'friends', g) == 0 then
    status = 'UNKNOWN'
  elseif state == 'claimed' or state == 'working' then
    status, detail = 'LIVE', 'owner=' .. (redis.call('HGET', key, 'owner') or '') .. ' needs assign --revoke'
  elseif state ~= 'open' then
    status, detail = 'CLOSED', 'state=' .. state
  end
  if not status then
    local eligible = (kind == 'read' or kind == 'review') and fr_has_role(g, 'may-hold') or
      ((kind ~= 'read' and kind ~= 'review') and (fr_has_role(g, 'builder') or fr_has_role(g, 'coordinator')))
    if not eligible then status, detail = 'NOROUTE', 'target has no role for ' .. kind end
  end
  local from = nil
  local score = nil
  if not status then
    from, score = ra_queue_of(S, id, ctx.friends)
    if from == g then
      status, detail = 'SAME', 'on ' .. g
    elseif ctx.up[g] == nil then
      ctx.up[g] = rd_free(g, ctx.sprints) ~= nil
    end
  end
  if not status and not ctx.up[g] then
    status = 'DOWN'
  end
  if not status then
    if kind == 'read' or kind == 'review' then
      if rd_author(S, key) == g then
        status = 'AUTHOR'
      else
        local why = rd_dedup(S, g, key, id, ctx.held)
        if why then
          status, detail = 'DEDUP', why
        end
      end
    end
  end
  if not status then
    if not from then
      from, score = 'ready', tostring(redis.call('HGET', key, 'priority') or '0')
    end
    -- the one task move (NS.task): ready on g's queue at the same score
    local fields = { 'dest', g }
    if from ~= 'ready' then
      local marker = '[moved from ' .. from .. ': ' .. ctx.reason .. ']'
      fields = { 'dest', g, 'moved_from', from,
        'title', rd_mark(redis.call('HGET', key, 'title') or '', marker) }
    end
    NS.task.set(id, 'open', { friend = g, sprint = S, qscore = score, by = ctx.actor, why = ctx.reason ~= '' and ctx.reason or 'assign',
      fields = fields })
    rd_note_held(S, g, key, id, ctx.held)
    ctx.woken[g] = true
    status, detail = 'MOVED', 'from=' .. from
  end
  local attempt = state and (redis.call('HGET', key, 'attempt') or '0') or '0'
  rd_log(S, 'task assign', id, state or '', status == 'MOVED' and 'open' or (state or ''), attempt, '',
    ctx.actor, ctx.reason, status .. ' to=' .. g .. (detail ~= '' and (' ' .. detail) or ''), ctx.idem, ctx.at)
  return status, detail
end

local function assign_batch(keys, args)
  local S, reason, actor, idem = args[1], args[2], args[3], args[4]
  local actor_err = fr_actor(actor)
  if actor_err then return actor_err end
  if not S or S == '' or (#args - 4) % 2 ~= 0 then
    return { 'INVALID' }
  end
  local sstatus = redis.call('HGET', 's:' .. S, 'status')
  if sstatus ~= 'open' and sstatus ~= 'paused' then
    return { 'NOSPRINT' }
  end
  if not fr_roster() then return redis.error_reply('ERR NOROSTER') end
  if not reason or reason == '' then
    reason = 'assign'
  end
  local friends = redis.call('SMEMBERS', 'friends')
  table.sort(friends)
  local ctx = {
    S = S, reason = reason, actor = actor, idem = idem, at = rd_now_ms(),
    friends = friends, sprints = rd_open_sprints(), up = {}, held = {}, woken = {},
  }
  local out = { 'OK' }
  for i = 5, #args, 2 do
    local id, g = args[i], args[i + 1]
    local status, detail = ra_assign_one(ctx, id, g)
    out[#out + 1] = id
    out[#out + 1] = g
    out[#out + 1] = status
    out[#out + 1] = detail
  end
  ra_wake(ctx.woken, ctx.at, actor, idem, 'assigned')
  return out
end

local function redistribute_from(keys, args)
  local f, reason, to, kinds_csv = args[1], args[2], args[3] or '', args[4] or ''
  local actor, idem = args[5], args[6]
  local actor_err = fr_actor(actor)
  if actor_err then return actor_err end
  local roster = fr_roster()
  if not roster then return redis.error_reply('ERR NOROSTER') end
  local mayhold, builders, coord = roster.mayhold, roster.builders, roster.coordinator
  if not f or f == '' or not reason or reason == '' or to == f then
    return { 'INVALID' }
  end
  if redis.call('SISMEMBER', 'friends', f) == 0 or
      (to ~= '' and redis.call('SISMEMBER', 'friends', to) == 0) then
    return { 'NOTFOUND' }
  end
  local at = rd_now_ms()
  local sprints = rd_open_sprints()
  local free, seen = {}, {}
  for _, list in ipairs({ mayhold, builders, { coord }, { to } }) do
    for _, g in ipairs(list) do
      if g ~= '' and not seen[g] then
        seen[g] = true
        free[g] = rd_free(g, sprints)
      end
    end
  end
  local kinds = nil
  if kinds_csv ~= '' then
    kinds = {}
    for _, k in ipairs(rd_csv(kinds_csv)) do kinds[k] = true end
  end
  local skey = 'friend:' .. f .. ':state'
  local state = redis.call('HGET', skey, 'state') or ''
  local up = redis.call('EXISTS', 'friend:' .. f .. ':beat') == 1
  -- The tick's expired-window rule (ns_friend_redistribute): an up friend
  -- whose out-of-credits until has passed is back, so the state is cleared
  -- and its live leases are never fenced by hand.
  if state == RD_OUT and up then
    local until_ms = tonumber(redis.call('HGET', skey, 'until') or '')
    if until_ms and at >= until_ms then
      -- friend.lua's fs_clear is the key's one writer (#3101); it logs the
      -- same friend-state-clear line (<had> window reset).
      fs_clear(f, 'window reset', actor, idem, at)
      state = ''
    end
  end
  local ctx = {
    f = f, why = reason, marker = '[moved from ' .. f .. ': ' .. reason .. ']',
    mayhold = mayhold, builders = builders, coord = coord, free = free, woken = {},
    sprints = sprints, actor = actor, idem = idem, at = at, held = {}, events = {},
    kinds = kinds, to = (to ~= '' and to or nil), to_list = (to ~= '' and { to } or nil),
    log_kind = 'task assign',
    moved = 0, leases = 0, released = 0, unrouted = 0, kept = 0,
  }
  if state == RD_OUT or not up then
    rd_close_leases(ctx)
  end
  rd_move_open(ctx)
  -- #4145: a down friend's cards (friend:<f>:down, or no beat) leave its
  -- queue too: every ready card and every lapsed lease, by the deal's one
  -- rebalance (deal_friend.lua), to the up friends or the stream's ready
  -- set. A kinds filter keeps the hand move to the sprint queues above.
  if not kinds and (not up or redis.call('EXISTS', 'friend:' .. f .. ':down') == 1) then
    local targets = {}
    if to ~= '' then
      targets = { to }
    else
      for _, g in ipairs(redis.call('SMEMBERS', 'friends')) do
        if g ~= f and rd_free(g, sprints) ~= nil then targets[#targets + 1] = g end
      end
      table.sort(targets)
    end
    local moves, refused, nready = DF.rebalance(f, targets, actor)
    if nready > 0 and #moves == 0 then
      local r = DF.rebalance_reply(f, moves, refused, nready)
      return { 'REFUSED', r[2] }
    end
    for i = 1, #moves, 2 do
      local ev = ctx.events
      ev[#ev + 1], ev[#ev + 1], ev[#ev + 1], ev[#ev + 1] = 'MOVED', moves[i], moves[i + 1], ctx.marker
      if moves[i + 1] == 'ready' then
        ctx.unrouted = ctx.unrouted + 1
      else
        ctx.moved = ctx.moved + 1
        ctx.woken[moves[i + 1]] = true
      end
    end
  end
  ra_wake(ctx.woken, at, actor, idem, 'redistributed')
  rd_caplog('redistribute', f, reason .. ' moved=' .. ctx.moved .. ' kept=' .. ctx.kept, actor, idem, at)
  return { 'OK', { f, state, tostring(ctx.moved), tostring(ctx.leases), tostring(ctx.released),
    tostring(ctx.unrouted), tostring(ctx.kept) }, ctx.events }
end

redis.register_function('ns_assign_batch', assign_batch)
redis.register_function('ns_redistribute_from', redistribute_from)
