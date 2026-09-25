-- Out-of-credits and down friends are redistributed by the sprint tick
-- (nova-tools #3047, mechanism 1 of #3033; replaces the #2756 5.2 sentence
-- "work assigned to a friend who goes down stays with that friend until
-- `assign` moves it"). No shebang: loader.go prepends the single library
-- header. loader.go wraps this file in its own do-block; friend.lua's helpers
-- arrive through NS.friend and the rd_ helpers redistribute_assign.lua calls
-- leave through NS.redistribute (both at the ends of the two files).
--
-- Keys:
--   friend:<f>:state   hash, written only through friend.lua's fs_set and
--                      fs_clear (ns_friend_state, #3101). This tick reads it
--                      and, through those same two functions, writes state
--                      `down` when the presence beat friend:<f>:beat is stale (NS.beat)
--                      with work still on f, and clears a state whose reason is
--                      over (a beat back, an out-of-credits or away `until`
--                      passed with the friend up). `out-of-credits` and `away`
--                      come from `friend report`; `idle` at rung 3 from the
--                      ladder (friend.lua).
--
-- ns_friend_redistribute is one atomic call over every registered friend:
--   * out-of-credits, away, wake-missed (beat or not), or idle at rung 3:
--     every open task on f's queues
--     and every lease f holds is moved in this same call.
--   * presence stale with open work or leases: the first tick writes
--     state=down and moves nothing; the next tick that still finds no beat
--     moves everything (one tick later). A beat back before then clears it.
--   * a moved read goes to the least-loaded UP, unpaused, stateless may-hold
--     reader with free width who is neither f nor the PR's author; a build or
--     fix goes to another builder with free width, else to the coordinator's
--     queue; with no eligible consumer the task goes to `ready` (never to a
--     down friend, never dropped).
--   * a lease f held is closed with evidence (receipt `task lease-close`), its
--     token fenced so the old attempt's done and beat refuse, and the task is
--     requeued by the same rule; an external-effect task goes to
--     reconcile-required instead (spec 3.1 row 9), never replayed.
--   * a read of a PR on which f carries a typed HOLD cannot be answered by
--     anyone but f, so it is cancelled and one `release-<repo>-<pr>` task is
--     pushed to a may-hold non-author reader with the nova-merge recipe.
--   * every moved title carries `[moved from <f>: <why>]`; each receiving
--     friend gets one wake. No model and no coordinator is called.
-- Free width = desired slots - leased - open tasks queued, over open sprints.

local FS_AWAY, FS_IDLE, FS_UNDER = NS.friend.FS_AWAY, NS.friend.FS_IDLE, NS.friend.FS_UNDER
-- wake-missed (#3153) is a down state for moving work that a beat never
-- clears: the shell beats while the model is gone. Only the life
-- classifier's conditional clear ends it.
local RD_WAKE_MISSED = NS.friend.FS_WAKE_MISSED
local fs_blocks, fs_set, fs_clear = NS.friend.fs_blocks, NS.friend.fs_set, NS.friend.fs_clear
local fr_has_role, fr_roster = NS.friend_roles.fr_has_role, NS.friend_roles.fr_roster

local RD_OUT = 'out-of-credits'
local RD_DOWN = 'down'

local function rd_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function rd_log(S, kind, id, from_state, to_state, attempt, token_sha, actor, reason, evidence, idem, at)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
    'attempt', tostring(attempt or 0), 'token_sha', token_sha or '',
    'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
    'idem', idem or '', 'at', tostring(at))
end

local function rd_caplog(kind, subject, reason, actor, idem, at)
  redis.call('XADD', 'cap:log', '*',
    'kind', kind, 'subject', subject, 'reason', reason or '',
    'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
end

local function rd_csv(s)
  local out = {}
  for name in string.gmatch(s or '', '[^,]+') do
    out[#out + 1] = name
  end
  table.sort(out)
  return out
end

local function rd_open_sprints()
  local out = {}
  for _, S in ipairs(redis.call('SMEMBERS', 'sprints')) do
    local status = redis.call('HGET', 's:' .. S, 'status')
    if status == 'open' or status == 'paused' then
      out[#out + 1] = S
    end
  end
  table.sort(out)
  return out
end

-- rd_free is nil for a friend who cannot receive work (unregistered, no beat,
-- paused, or in a state that bars routing: fs_blocks), else its free width.
local function rd_free(g, sprints)
  if redis.call('SISMEMBER', 'friends', g) == 0 or
      not NS.beat.live('friend:' .. g .. ':beat') or
      redis.call('HGET', 'friend:' .. g .. ':desired', 'paused') == '1' or
      fs_blocks(g) then
    return nil
  end
  local free = tonumber(redis.call('HGET', 'friend:' .. g .. ':desired', 'slots') or '0') or 0
  free = free - redis.call('ZCARD', 'friend:' .. g .. ':starting') -
    redis.call('ZCARD', 'friend:' .. g .. ':living')
  for _, S in ipairs(sprints) do
    free = free - redis.call('ZCARD', 's:' .. S .. ':open:' .. g)
  end
  return free
end

-- rd_pick: the candidate with the most free width above zero; names are
-- sorted, so ties go to the first name.
local function rd_pick(cands, exclude, free)
  local best, best_free = nil, 0
  for _, g in ipairs(cands) do
    if not exclude[g] and free[g] ~= nil and free[g] > best_free then
      best, best_free = g, free[g]
    end
  end
  return best
end

local function rd_has_work(f, sprints)
  if redis.call('ZCARD', 'friend:' .. f .. ':starting') + redis.call('ZCARD', 'friend:' .. f .. ':living') > 0 then
    return true
  end
  for _, S in ipairs(sprints) do
    if redis.call('ZCARD', 's:' .. S .. ':open:' .. f) > 0 then
      return true
    end
  end
  return false
end

local function rd_mark(title, marker)
  if string.find(title, marker, 1, true) then
    return title
  end
  if title == '' then
    return marker
  end
  return title .. ' ' .. marker
end

local function rd_author(S, key)
  local author = redis.call('HGET', key, 'author')
  if author and author ~= '' then
    return author
  end
  local repo = redis.call('HGET', key, 'repo') or ''
  local pr = redis.call('HGET', key, 'pr') or ''
  if repo == '' or pr == '' then
    return ''
  end
  return redis.call('HGET', 's:' .. S .. ':pr:' .. repo .. ':' .. pr, 'author') or ''
end

-- rd_held lists, once per call and per friend g, the canonical read
-- identities (repo|pr|full head) g already holds in sprint S: a closed read
-- (s:S:done:g), a claimed or working one (g's leases) and one open on g's
-- queue. Spec 5.7 (6): a friend holding any of these never receives the same
-- identity again.
local function rd_held(S, g, cache)
  local ck = S .. '\31' .. g
  if cache[ck] then
    return cache[ck]
  end
  local held = {}
  local function note(id, how)
    local v = redis.call('HMGET', 's:' .. S .. ':task:' .. id, 'kind', 'repo', 'pr', 'head')
    if (v[1] == 'read' or v[1] == 'review') and v[2] and v[3] and v[4] and v[4] ~= '' then
      local ident = v[2] .. '|' .. v[3] .. '|' .. v[4]
      if not held[ident] then
        held[ident] = { id = id, how = how }
      end
    end
  end
  for _, id in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':done:' .. g)) do
    note(id, 'closed')
  end
  for _, zkey in ipairs({ 'friend:' .. g .. ':starting', 'friend:' .. g .. ':living' }) do
    for _, identity in ipairs(redis.call('ZRANGE', zkey, 0, -1)) do
      local LS, id = string.match(identity, '^([^/]+)/(.+)/%d+$')
      if LS == S then
        note(id, 'working')
      end
    end
  end
  for _, id in ipairs(redis.call('ZRANGE', 's:' .. S .. ':open:' .. g, 0, -1)) do
    note(id, 'open')
  end
  cache[ck] = held
  return held
end

-- rd_dedup is the transfer dedup of spec 5.7 (6) on (repo, PR, full head,
-- friend): the reason g may not receive read id, or nil. A task that is not a
-- read, or carries no head, is never deduplicated.
local function rd_dedup(S, g, key, id, cache)
  local v = redis.call('HMGET', key, 'kind', 'repo', 'pr', 'head')
  if (v[1] ~= 'read' and v[1] ~= 'review') or not v[2] or not v[3] or not v[4] or v[4] == '' then
    return nil
  end
  if redis.call('HEXISTS', 's:' .. S .. ':disp:' .. v[2] .. ':' .. v[3], g .. '@' .. v[4]) == 1 then
    return g .. ' posted a typed line at ' .. v[4]
  end
  local h = rd_held(S, g, cache)[v[2] .. '|' .. v[3] .. '|' .. v[4]]
  if h and h.id ~= id then
    return g .. ' holds ' .. h.how .. ' ' .. h.id .. ' at ' .. v[4]
  end
  return nil
end

-- rd_note_held records that g now holds read id on its open queue, so a
-- second line for the same identity in the same call is deduplicated too.
local function rd_note_held(S, g, key, id, cache)
  local v = redis.call('HMGET', key, 'kind', 'repo', 'pr', 'head')
  if (v[1] == 'read' or v[1] == 'review') and v[2] and v[3] and v[4] and v[4] ~= '' then
    local held = rd_held(S, g, cache)
    local ident = v[2] .. '|' .. v[3] .. '|' .. v[4]
    if not held[ident] then
      held[ident] = { id = id, how = 'open' }
    end
  end
end

-- rd_carried_hold: f recorded a typed HOLD on this PR (at any head) and has
-- no APPROVE at the task's head. Returns the disposition field of the HOLD.
local function rd_carried_hold(S, f, key)
  local repo = redis.call('HGET', key, 'repo') or ''
  local pr = redis.call('HGET', key, 'pr') or ''
  local head = redis.call('HGET', key, 'head') or ''
  if repo == '' or pr == '' then
    return nil
  end
  local disp = redis.call('HGETALL', 's:' .. S .. ':disp:' .. repo .. ':' .. pr)
  local hold, approved = nil, false
  local prefix = f .. '@'
  for i = 1, #disp, 2 do
    local field, value = disp[i], disp[i + 1]
    if string.sub(field, 1, #prefix) == prefix then
      local verdict = string.upper(string.match(value, '^(%S+)') or '')
      if verdict == 'HOLD' then
        hold = field
      elseif verdict == 'APPROVE' and field == prefix .. head then
        approved = true
      end
    end
  end
  if approved then
    return nil
  end
  return hold
end

local function rd_place(S, id, score, target)
  if target then
    redis.call('ZADD', 's:' .. S .. ':open:' .. target, score, id)
  else
    redis.call('ZADD', 's:' .. S .. ':ready', score, id)
  end
end

-- rd_release cancels a read that only f could answer and pushes one release
-- task for the PR (deduplicated by id) to a may-hold non-author reader.
local function rd_release(ctx, S, id, score, key, hold)
  local f = ctx.f
  local repo = redis.call('HGET', key, 'repo')
  local pr = redis.call('HGET', key, 'pr')
  local head = redis.call('HGET', 's:' .. S .. ':pr:' .. repo .. ':' .. pr, 'head') or
    redis.call('HGET', key, 'head') or ''
  local rid = 'release-' .. repo .. '-' .. pr
  local rkey = 's:' .. S .. ':task:' .. rid
  local target = nil
  if redis.call('EXISTS', rkey) == 0 then
    local exclude = { [f] = true }
    local author = rd_author(S, key)
    if author ~= '' then exclude[author] = true end
    target = rd_pick(ctx.mayhold, exclude, ctx.free)
    local title = 'release ' .. repo .. '#' .. pr .. ': ' .. f .. ' carries a typed HOLD (' .. hold ..
      ') and is ' .. ctx.why .. '; a may-hold reader releases it by lane record: nova-merge read --pr ' ..
      pr .. ' --who <you> --head ' .. head .. ' --verdict approve --scope "release ' .. f ..
      ' HOLD" --releases record:<at of the HOLD> ' .. ctx.marker
    local priority = redis.call('HGET', key, 'priority') or '0'
    local ref = redis.call('HGET', key, 'ref') or ''
    local payload = redis.sha1hex(table.concat({ 'read', repo, ref, pr, head, title }, '\31'))
    redis.call('HSET', rkey,
      'kind', 'read', 'repo', repo, 'ref', ref, 'pr', pr, 'head', head,
      'title', title, 'effects', 'none', 'owner', '', 'priority', priority,
      'state', 'open', 'attempt', '0', 'token', '0', 'payload_sha', payload,
      'reason', 'release', 'evidence', '', 'claimed_at', '', 'started_at', '',
      'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '',
      'moved_from', f, 'hold', hold)
    rd_place(S, rid, score, target)
    redis.call('SADD', 's:' .. S .. ':idx:task:open', rid)
    rd_log(S, 'task push', rid, '', 'open', 0, '', ctx.actor, 'release', 'hold=' .. hold .. ' to=' .. (target or 'ready'), ctx.idem, ctx.at)
    if target then
      ctx.free[target] = ctx.free[target] - 1
      ctx.woken[target] = true
    else
      ctx.unrouted = ctx.unrouted + 1
    end
  end
  redis.call('ZREM', 's:' .. S .. ':open:' .. f, id)
  redis.call('SREM', 's:' .. S .. ':idx:task:open', id)
  redis.call('SADD', 's:' .. S .. ':idx:task:cancelled', id)
  redis.call('HSET', key, 'state', 'cancelled', 'reason', 'carried HOLD: ' .. rid,
    'title', rd_mark(redis.call('HGET', key, 'title') or '', ctx.marker), 'moved_from', f)
  rd_log(S, 'task cancel', id, 'open', 'cancelled', redis.call('HGET', key, 'attempt') or '0', '', ctx.actor,
    'carried HOLD: ' .. rid, 'hold=' .. hold, ctx.idem, ctx.at)
  ctx.released = ctx.released + 1
end

-- rd_event records one line of what a hand redistribute did, for the verb.
local function rd_event(ctx, what, id, a, b)
  if ctx.events then
    local ev = ctx.events
    ev[#ev + 1] = what
    ev[#ev + 1] = id
    ev[#ev + 1] = a or ''
    ev[#ev + 1] = b or ''
  end
end

-- rd_route moves one open task off f by kind. requeue is true for a task
-- whose lease was just closed: it must be placed, so the kind filter and the
-- keep-on-f outcome of a hand move never apply to it.
local function rd_route(ctx, S, id, score, requeue)
  local f = ctx.f
  local key = 's:' .. S .. ':task:' .. id
  local kind = redis.call('HGET', key, 'kind') or 'work'
  if ctx.kinds and not requeue and not ctx.kinds[kind] then
    return
  end
  local exclude = { [f] = true }
  local cands
  if kind == 'read' or kind == 'review' then
    local hold = rd_carried_hold(S, f, key)
    if hold then
      rd_release(ctx, S, id, score, key, hold)
      return
    end
    local author = rd_author(S, key)
    if author ~= '' then exclude[author] = true end
    cands = ctx.to_list or ctx.mayhold
    for _, g in ipairs(cands) do
      if not exclude[g] then
        local why = rd_dedup(S, g, key, id, ctx.held)
        if why then
          exclude[g] = true
          rd_event(ctx, 'DEDUP', id, g, why)
        end
      end
    end
  else
    cands = ctx.to_list or ctx.builders
  end
  local target
  if ctx.to then
    local role_ok = (kind == 'read' or kind == 'review') and fr_has_role(ctx.to, 'may-hold') or
      ((kind ~= 'read' and kind ~= 'review') and
        (fr_has_role(ctx.to, 'builder') or fr_has_role(ctx.to, 'coordinator')))
    if role_ok and not exclude[ctx.to] and ctx.free[ctx.to] ~= nil then
      target = ctx.to
    end
  else
    target = rd_pick(cands, exclude, ctx.free)
    if not target and kind ~= 'read' and kind ~= 'review' and ctx.coord ~= '' and
        ctx.coord ~= f and ctx.free[ctx.coord] ~= nil then
      target = ctx.coord
    end
  end
  if not target and ctx.to and not requeue then
    ctx.kept = ctx.kept + 1
    rd_event(ctx, 'KEPT', id, f, ctx.to .. ' cannot receive it')
    return
  end
  redis.call('ZREM', 's:' .. S .. ':open:' .. f, id)
  rd_place(S, id, score, target)
  redis.call('HSET', key, 'owner', '', 'moved_from', f,
    'title', rd_mark(redis.call('HGET', key, 'title') or '', ctx.marker))
  rd_log(S, ctx.log_kind or 'task move', id, 'open', 'open', redis.call('HGET', key, 'attempt') or '0', '', ctx.actor,
    ctx.marker, 'to=' .. (target or 'ready'), ctx.idem, ctx.at)
  rd_event(ctx, 'MOVED', id, target or 'ready', ctx.marker)
  if target then
    ctx.free[target] = ctx.free[target] - 1
    ctx.woken[target] = true
    ctx.moved = ctx.moved + 1
    rd_note_held(S, target, key, id, ctx.held)
  else
    ctx.unrouted = ctx.unrouted + 1
  end
end

-- rd_close_leases fences and closes every lease f holds, then requeues.
local function rd_close_leases(ctx)
  local f = ctx.f
  for _, zkey in ipairs({ 'friend:' .. f .. ':starting', 'friend:' .. f .. ':living' }) do
    for _, identity in ipairs(redis.call('ZRANGE', zkey, 0, -1)) do
      redis.call('ZREM', zkey, identity)
      ctx.leases = ctx.leases + 1
      local evidence = 'lease ' .. identity .. ' closed: ' .. f .. ' ' .. ctx.why
      local S, id, attempt = string.match(identity, '^([^/]+)/(.+)/(%d+)$')
      local key = S and ('s:' .. S .. ':task:' .. id) or ''
      local state = S and redis.call('HGET', key, 'state') or nil
      if state and (state == 'claimed' or state == 'working') and
          redis.call('HGET', key, 'owner') == f and redis.call('HGET', key, 'attempt') == attempt then
        local token_sha = redis.call('HGET', key, 'token_sha') or ''
        redis.call('HSET', key, 'token', 'fenced')
        redis.call('SREM', 's:' .. S .. ':idx:task:' .. state, id)
        if (redis.call('HGET', key, 'effects') or 'none') == 'external' then
          redis.call('HSET', key, 'state', 'reconcile-required', 'reason', evidence)
          redis.call('SADD', 's:' .. S .. ':idx:task:reconcile-required', id)
          redis.call('HSET', 's:' .. S .. ':unresolved', id .. ':lease-close:',
            'state=' .. state .. ' attempt=' .. attempt .. ' token_sha=' .. token_sha ..
            ' reason=' .. f .. ' ' .. ctx.why .. ' at=' .. tostring(ctx.at))
          rd_log(S, 'task lease-close', id, state, 'reconcile-required', attempt, token_sha, ctx.actor,
            ctx.marker, evidence, ctx.idem, ctx.at)
        else
          redis.call('HSET', key, 'state', 'open', 'reason', evidence)
          redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
          rd_log(S, 'task lease-close', id, state, 'open', attempt, token_sha, ctx.actor,
            ctx.marker, evidence, ctx.idem, ctx.at)
          rd_route(ctx, S, id, tonumber(redis.call('HGET', key, 'priority') or '0') or 0, true)
        end
      else
        rd_caplog('lease-dropped', f, evidence, ctx.actor, ctx.idem, ctx.at)
      end
    end
  end
end

local function rd_move_open(ctx)
  for _, S in ipairs(ctx.sprints) do
    local items = redis.call('ZRANGE', 's:' .. S .. ':open:' .. ctx.f, 0, -1, 'WITHSCORES')
    for i = 1, #items, 2 do
      rd_route(ctx, S, items[i], tonumber(items[i + 1]) or 0, false)
    end
  end
end

-- ns_friend_redistribute: args = actor, idem. The routing roster is read from
-- friend:<f>:roles for every member of friends in this same call.
-- friend name state moved leases released unrouted pending.
local function friend_redistribute(keys, args)
  local actor, idem = args[1], args[2]
  local roster = fr_roster()
  if not roster then return redis.error_reply('ERR NOROSTER') end
  local mayhold, builders, coord = roster.mayhold, roster.builders, roster.coordinator
  local at = rd_now_ms()
  local sprints = rd_open_sprints()
  local free = {}
  local seen = {}
  for _, list in ipairs({ mayhold, builders, { coord } }) do
    for _, g in ipairs(list) do
      if g ~= '' and not seen[g] then
        seen[g] = true
        free[g] = rd_free(g, sprints)
      end
    end
  end
  local woken, held = {}, {}
  local friends = redis.call('SMEMBERS', 'friends')
  table.sort(friends)
  local out = {}
  for _, f in ipairs(friends) do
    local st = redis.call('HMGET', 'friend:' .. f .. ':state', 'state', 'until', 'rung')
    local state = st[1]
    local up = NS.beat.live('friend:' .. f .. ':beat')
    local why, pending = nil, false
    if (state == FS_IDLE or state == FS_UNDER) and not up then
      -- a ladder state whose beat expired is judged as any absent friend
      state = nil
    end
    if state == RD_OUT or state == FS_AWAY then
      local until_ms = tonumber(st[2] or '')
      if up and until_ms and at >= until_ms then
        fs_clear(f, state .. ' window over', actor, idem, at)
        state = nil
      elseif state == RD_OUT then
        why = 'out of credits'
      else
        why = 'away'
      end
    elseif state == RD_WAKE_MISSED then
      why = 'wake-missed'
    elseif state == RD_DOWN then
      if up then
        fs_clear(f, 'down: beat returned', actor, idem, at)
        state = nil
      else
        why = 'down'
      end
    elseif state == FS_IDLE and (tonumber(st[3]) or 0) >= 3 then
      -- rung 3 of the idle ladder (5.5): as down. underfull stops at rung 2.
      why = 'idle'
    elseif not up and rd_has_work(f, sprints) then
      fs_set(f, RD_DOWN, '', 'presence expired with work', 0, 0, 0, actor, idem, at, false)
      state, pending = RD_DOWN, true
    end
    if why or pending then
      local ctx = {
        f = f, why = why or 'down', marker = '[moved from ' .. f .. ': ' .. (why or 'down') .. ']',
        mayhold = mayhold, builders = builders, coord = coord, free = free, woken = woken,
        sprints = sprints, actor = actor, idem = idem, at = at, held = held,
        moved = 0, leases = 0, released = 0, unrouted = 0, kept = 0,
      }
      if why then
        rd_close_leases(ctx)
        rd_move_open(ctx)
      end
      for _, v in ipairs({ 'friend', f, state, tostring(ctx.moved), tostring(ctx.leases),
          tostring(ctx.released), tostring(ctx.unrouted), pending and '1' or '0' }) do
        out[#out + 1] = v
      end
    end
  end
  local targets = {}
  for g in pairs(woken) do targets[#targets + 1] = g end
  table.sort(targets)
  for _, g in ipairs(targets) do
    local wake = 'friend:' .. g .. ':wake'
    redis.call('LPUSH', wake, tostring(at) .. ':redistributed')
    redis.call('LTRIM', wake, 0, 0)
    rd_caplog('friend-wake', g, 'redistributed', actor, idem, at)
  end
  return out
end

redis.register_function('ns_friend_redistribute', friend_redistribute)

-- The cross-file surface redistribute_assign.lua imports (loader.go: every
-- file is its own do-block; NS is the one chunk-level local).
NS.redistribute = {
  RD_OUT = RD_OUT,
  rd_author = rd_author, rd_caplog = rd_caplog, rd_close_leases = rd_close_leases,
  rd_csv = rd_csv, rd_dedup = rd_dedup, rd_free = rd_free, rd_log = rd_log,
  rd_mark = rd_mark, rd_move_open = rd_move_open, rd_note_held = rd_note_held,
  rd_now_ms = rd_now_ms, rd_open_sprints = rd_open_sprints,
  rd_carried_hold = rd_carried_hold, rd_route = rd_route,
}
