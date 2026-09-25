-- Friend state and the declared wake path (nova-tools #3101; spec #2756 v6
-- 2.2, 4.4, 5.5). No shebang: loader.go prepends the single library header.
-- loader.go wraps this file in its own do-block, so its locals are its own;
-- the helpers redistribute.lua and redistribute_assign.lua call (fs_set,
-- fs_clear, fs_blocks and three state names) are exported below as NS.friend.
--
-- friend:<f>:state (hash) has ONE writer: the fs_set/fs_clear pair below,
-- reached from outside only as ns_friend_state (and from the redistribute
-- tick in the same library). internal/nsprint/friend's TestControl43OneWriter
-- fails on a write to the key from any other file. Fields:
--   state   out-of-credits | away | down | idle | underfull | offline-model |
--           wake-missed (absent = up); the last two come from the life
--           classifier (#3153, actor life, idem life:<f>:...)
--   since   when this state began (server ms); kept across rung changes
--   until   out-of-credits reset or end of an away (ms, or '')
--   rung    ladder step 0-3 (5.5); lives in the store, so a restarted
--           reconciler continues at the rung it left (Stella's HOLD7 on #3058)
--   ticks   sweeps observed at this rung
--   living  leases seen by the last observation; a rise is a take, and a
--           take at any rung resets the rung to 0
--   reason, at, idem (the last applied call; a replay is answered, not applied)
--
-- A rung action is taken in the same call that raises the rung, so a
-- restarted reconciler or a replayed call can never send it twice:
--   rung 1  friend:outbox kind=nudge (to the friend's bus channel)
--   rung 2  unit wake path: one wake on friend:<f>:wake and kind=wake;
--           human: one kind=notice to the declared notify channel with the
--           open count; none declared: kind=wake-missing
--   rung 3  (idle only) the redistribute tick moves the friend's work; an
--           underfull friend has a live child and never reaches rung 3.
-- friend:outbox is the zero-token outbox a bus relay reads; no model is called.
--
-- friend:<f>:wakepath (hash; kind unit|human, unit, host, notify,
-- declared_at) is written by ns_friend_wakepath from `capacity friend <f> --as <actor>
-- --wake` (config, etc/friends.conf).
--
-- friend:<f>:wakemode (hash; mode, deliver, turn, lag_ms, actor, declared_at;
-- #3153) is written only by ns_friend_wakemode, which re-reads the firing
-- receipt from friend:<f>:events itself. Absent = active-turn/tool-return.

local FS_OUT = 'out-of-credits'
local FS_AWAY = 'away'
local FS_DOWN = 'down'
local FS_IDLE = 'idle'
local FS_UNDER = 'underfull'
local FS_OUTBOX = 'friend:outbox'
local FS_OFFLINE = 'offline-model'
local FS_WAKE_MISSED = 'wake-missed'
local FS_LIFE = 'life'
local FS_WAKE_TTL_MS = 600000
local FS_RECEIPT_MS = 120000
-- Held states belong to the report, the life classifier and the redistribute
-- tick; the ladder's observation never overwrites them, and each blocks new
-- work (fs_blocks).
local FS_HELD = { [FS_OUT] = true, [FS_AWAY] = true, [FS_DOWN] = true,
  [FS_OFFLINE] = true, [FS_WAKE_MISSED] = true }

local function fs_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function fs_key(f)
  return 'friend:' .. f .. ':state'
end

local function fs_caplog(kind, subject, reason, actor, idem, at)
  redis.call('XADD', 'cap:log', '*',
    'kind', kind, 'subject', subject, 'reason', reason or '',
    'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
end

-- fs_blocks: the state bars f from receiving work (reader and builder choice,
-- 5.5): down, out-of-credits, away, or idle past rung 2.
local function fs_blocks(f)
  local h = redis.call('HMGET', fs_key(f), 'state', 'rung')
  local s = h[1]
  if not s then
    return false
  end
  if FS_HELD[s] then
    return true
  end
  return s == FS_IDLE and (tonumber(h[2]) or 0) >= 3
end

-- fs_set writes one state. since is kept while the state is unchanged, and
-- across idle <-> underfull within one episode (keep_since).
local function fs_set(f, state, until_ms, reason, rung, ticks, living, actor, idem, at, keep_since)
  local key = fs_key(f)
  local cur = redis.call('HMGET', key, 'state', 'since')
  local since = cur[2]
  if not since or (cur[1] ~= state and not keep_since) then
    since = tostring(at)
  end
  redis.call('HSET', key, 'state', state, 'since', since, 'until', until_ms or '',
    'rung', tostring(rung or 0), 'ticks', tostring(ticks or 0), 'living', tostring(living or 0),
    'reason', reason or '', 'at', tostring(at), 'idem', idem or '')
  if cur[1] ~= state then
    fs_caplog('friend-state', f, state .. ' ' .. (reason or ''), actor, idem, at)
  end
end

local function fs_clear(f, reason, actor, idem, at)
  local key = fs_key(f)
  local had = redis.call('HGET', key, 'state')
  if not had then
    return
  end
  redis.call('DEL', key)
  fs_caplog('friend-state-clear', f, had .. ' ' .. (reason or ''), actor, idem, at)
end

local function fs_outbox(f, kind, rung, channel, open, detail, actor, idem, at)
  redis.call('XADD', FS_OUTBOX, 'MAXLEN', '~', 100000, '*',
    'friend', f, 'kind', kind, 'rung', tostring(rung), 'channel', channel or '',
    'open', tostring(open or 0), 'detail', detail or '',
    'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
end

-- fs_rung_action takes the one action of a newly reached rung and names it.
local function fs_rung_action(f, rung, open, actor, idem, at)
  if rung == 1 then
    fs_outbox(f, 'nudge', 1, 'bus:To:' .. f, open, '', actor, idem, at)
    return 'nudge'
  end
  if rung == 2 then
    local wp = redis.call('HMGET', 'friend:' .. f .. ':wakepath', 'kind', 'unit', 'host', 'notify')
    if wp[1] == 'unit' then
      local wake = 'friend:' .. f .. ':wake'
      redis.call('LPUSH', wake, tostring(at) .. ':ladder')
      redis.call('LTRIM', wake, 0, 0)
      redis.call('PEXPIRE', wake, FS_WAKE_TTL_MS)
      fs_outbox(f, 'wake', 2, '', open, 'unit:' .. wp[2] .. '@' .. wp[3], actor, idem, at)
      return 'wake-unit'
    end
    if wp[1] == 'human' then
      fs_outbox(f, 'notice', 2, wp[4], open,
        f .. ' idle with ' .. tostring(open) .. ' open; waiting on human', actor, idem, at)
      return 'notice'
    end
    fs_outbox(f, 'wake-missing', 2, '', open, 'no declared wake path', actor, idem, at)
    return 'wake-missing'
  end
  if rung == 3 then
    return 'redistribute'
  end
  return ''
end

-- fs_entry_wake is the one wake sent when the life classifier first enters
-- offline-model: the rung-2 unit wake path (friend:<f>:wake and a kind=wake
-- on the outbox). A delivery that no caused turn-start answers within 120 s
-- becomes wake-missed on the classifier's next tick.
local function fs_entry_wake(f, actor, idem, at)
  local wake = 'friend:' .. f .. ':wake'
  redis.call('LPUSH', wake, tostring(at) .. ':offline-model')
  redis.call('LTRIM', wake, 0, 0)
  redis.call('PEXPIRE', wake, FS_WAKE_TTL_MS)
  fs_outbox(f, 'wake', 2, '', 0, 'offline-model', actor, idem, at)
  return 'wake'
end

-- fs_life_check is the atomic re-check of a classifier write (actor life):
-- args 7-8 are the state (or up) and idem (or '') the caller read. A
-- difference answers STALE; a report (present, not idle/underfull, idem not
-- life:) answers HELD; either way nothing is written. nil means go on.
local function fs_life_check(key, state, args)
  if args[7] == nil or args[8] == nil then
    return { 'INVALID' }
  end
  if state ~= 'clear' and state ~= FS_OUT and state ~= FS_DOWN and
      state ~= FS_OFFLINE and state ~= FS_WAKE_MISSED then
    return { 'INVALID' }
  end
  local cur = redis.call('HMGET', key, 'state', 'idem')
  local stored, sidem = cur[1] or 'up', cur[2] or ''
  if stored ~= args[7] or sidem ~= args[8] then
    return { 'STALE', stored, sidem }
  end
  if cur[1] and cur[1] ~= FS_IDLE and cur[1] ~= FS_UNDER and string.sub(sidem, 1, 5) ~= 'life:' then
    return { 'HELD', cur[1] }
  end
  return nil
end

-- fs_observe is one ladder step from the reconciler's observation.
-- obs is idle, underfull or up (nothing to climb: the ladder is cleared).
local function fs_observe(f, obs, open, living, policy_ticks, actor, idem, at)
  local key = fs_key(f)
  local cur = redis.call('HMGET', key, 'state', 'rung', 'ticks', 'living')
  local state = cur[1]
  if state and FS_HELD[state] then
    return { 'HELD', state, '0', '' }
  end
  if obs == 'up' then
    if state then
      fs_clear(f, 'no idle slot with open work', actor, idem, at)
    end
    return { 'OK', 'up', '0', '' }
  end
  local rung, ticks = 0, 0
  local prev_living = tonumber(cur[4] or '') or 0
  local keep = state == FS_IDLE or state == FS_UNDER
  if keep then
    rung, ticks = tonumber(cur[2]) or 0, tonumber(cur[3]) or 0
    if living > prev_living then
      -- a take: the rung resets and a new episode begins
      rung, ticks, keep = 0, 0, false
      fs_clear(f, 'take at rung ' .. (cur[2] or '0'), actor, idem, at)
    end
  end
  local max_rung = 3
  if obs == FS_UNDER then
    max_rung = 2
  end
  if rung > max_rung then
    rung = max_rung
  end
  ticks = ticks + 1
  local action = ''
  if ticks >= policy_ticks and rung < max_rung then
    rung, ticks = rung + 1, 0
    action = fs_rung_action(f, rung, open, actor, idem, at)
  end
  local reason = obs .. ': open ' .. tostring(open) .. ' living ' .. tostring(living)
  fs_set(f, obs, '', reason, rung, ticks, living, actor, idem, at, keep)
  return { 'OK', obs, tostring(rung), action }
end

-- ns_friend_state: args = friend, state, until (unix ms or ''), reason,
-- actor, idem, then for a ladder observation (state idle, underfull or up):
-- open, living, policy ticks. state out-of-credits, away, down,
-- offline-model and wake-missed are reports; clear removes any state. For
-- actor life (the #3153 classifier) args 7-8 are required: the state and idem
-- it read, re-checked here (fs_life_check). Returns OK state rung action,
-- HELD state (an observation or classifier write while a report holds), DUP
-- state rung (the idem was the last applied call), STALE state idem (the
-- stored state moved since the classifier read it), INVALID or NOTFOUND.
local function fs_friend_state(keys, args)
  local friend, state, until_ms, reason = args[1], args[2], args[3] or '', args[4] or ''
  local actor, idem = args[5], args[6] or ''
  if not friend or friend == '' or not state then
    return { 'INVALID' }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return { 'NOTFOUND' }
  end
  if until_ms ~= '' and not tonumber(until_ms) then
    return { 'INVALID' }
  end
  local key = fs_key(friend)
  if idem ~= '' then
    local last = redis.call('HMGET', key, 'idem', 'state', 'rung')
    if last[1] == idem then
      return { 'DUP', last[2] or '', last[3] or '0' }
    end
  end
  if actor == FS_LIFE then
    local answer = fs_life_check(key, state, args)
    if answer then
      return answer
    end
  end
  local at = fs_now_ms()
  if state == 'clear' then
    fs_clear(friend, reason, actor, idem, at)
    return { 'OK', 'up', '0', '' }
  end
  if state == FS_OUT or state == FS_DOWN or state == FS_WAKE_MISSED then
    fs_set(friend, state, until_ms, reason, 0, 0, 0, actor, idem, at, false)
    return { 'OK', state, '0', '' }
  end
  if state == FS_OFFLINE then
    local was = redis.call('HGET', key, 'state')
    fs_set(friend, state, until_ms, reason, 0, 0, 0, actor, idem, at, false)
    local action = ''
    if was ~= FS_OFFLINE then
      action = fs_entry_wake(friend, actor, idem, at)
    end
    return { 'OK', state, '0', action }
  end
  if state == FS_AWAY then
    if until_ms == '' then
      return { 'INVALID' }
    end
    fs_set(friend, state, until_ms, reason, 0, 0, 0, actor, idem, at, false)
    return { 'OK', state, '0', '' }
  end
  if state == FS_IDLE or state == FS_UNDER or state == 'up' then
    local open, living, ticks = tonumber(args[7] or ''), tonumber(args[8] or ''), tonumber(args[9] or '')
    if not open or not living or not ticks or ticks < 1 then
      return { 'INVALID' }
    end
    return fs_observe(friend, state, open, living, ticks, actor, idem, at)
  end
  return { 'INVALID' }
end

-- ns_friend_wakepath: args = friend, kind (unit, human or clear), unit label,
-- host, notify channel, actor, idem.
local function fs_friend_wakepath(keys, args)
  local friend, kind, unit, host, notify = args[1], args[2], args[3] or '', args[4] or '', args[5] or ''
  local actor, idem = args[6], args[7]
  if not friend or friend == '' then
    return { 'INVALID' }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return { 'NOTFOUND' }
  end
  local key = 'friend:' .. friend .. ':wakepath'
  local at = fs_now_ms()
  if kind == 'clear' then
    redis.call('DEL', key)
    fs_caplog('friend-wakepath', friend, 'clear', actor, idem, at)
    return { 'OK', '' }
  end
  if kind == 'unit' then
    if unit == '' or host == '' then
      return { 'INVALID' }
    end
    notify = ''
  elseif kind == 'human' then
    if notify == '' then
      return { 'INVALID' }
    end
    unit, host = '', ''
  else
    return { 'INVALID' }
  end
  redis.call('DEL', key)
  redis.call('HSET', key, 'kind', kind, 'unit', unit, 'host', host, 'notify', notify,
    'declared_at', tostring(at))
  fs_caplog('friend-wakepath', friend, kind .. ' ' .. unit .. '@' .. host .. ' ' .. notify, actor, idem, at)
  return { 'OK', kind }
end

-- fs_event reads one entry of friend:<f>:events as a field table, or nil.
local function fs_event(f, id)
  local r = redis.pcall('XRANGE', 'friend:' .. f .. ':events', id, id)
  if type(r) ~= 'table' or r.err or #r == 0 then
    return nil
  end
  local h, fields = {}, r[1][2]
  for i = 1, #fields, 2 do
    h[fields[i]] = fields[i + 1]
  end
  return h
end

-- ns_friend_wakemode: args = friend, deliver id D, turn-start id T, actor,
-- idem. Declares scheduled-model-turn only on a firing receipt it re-reads
-- itself: D is a deliver, T a turn-start with cause D, and 0 <= T.at - D.at
-- <= 120000 ms. Returns OK mode lag_ms, REFUSED why, INVALID or NOTFOUND.
local function fs_friend_wakemode(keys, args)
  local friend, did, tid, actor, idem = args[1], args[2], args[3], args[4] or '', args[5] or ''
  if not friend or friend == '' or not did or did == '' or not tid or tid == '' then
    return { 'INVALID' }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return { 'NOTFOUND' }
  end
  local d, t = fs_event(friend, did), fs_event(friend, tid)
  if not d or not t then
    return { 'REFUSED', 'no such event' }
  end
  if d.kind ~= 'deliver' or t.kind ~= 'turn-start' then
    return { 'REFUSED', 'want a deliver and a turn-start' }
  end
  if t.cause ~= did then
    return { 'REFUSED', 'turn-start cause is not the deliver' }
  end
  local lag = (tonumber(t.at or '') or -1) - (tonumber(d.at or '') or 0)
  if lag < 0 or lag > FS_RECEIPT_MS then
    return { 'REFUSED', 'lag ' .. tostring(lag) .. ' ms is outside 0..120000' }
  end
  local at = fs_now_ms()
  local key = 'friend:' .. friend .. ':wakemode'
  redis.call('DEL', key)
  redis.call('HSET', key, 'mode', 'scheduled-model-turn', 'deliver', did, 'turn', tid,
    'lag_ms', tostring(lag), 'actor', actor, 'declared_at', tostring(at))
  fs_caplog('friend-wakemode', friend, 'scheduled-model-turn ' .. did .. ' ' .. tid, actor, idem, at)
  return { 'OK', 'scheduled-model-turn', tostring(lag) }
end

redis.register_function('ns_friend_state', fs_friend_state)
redis.register_function('ns_friend_wakepath', fs_friend_wakepath)
redis.register_function('ns_friend_wakemode', fs_friend_wakemode)

-- The cross-file surface (loader.go: every file is its own do-block, and NS
-- is the one chunk-level local). redistribute*.lua sort after this file.
NS.friend = {
  FS_AWAY = FS_AWAY, FS_IDLE = FS_IDLE, FS_UNDER = FS_UNDER, FS_WAKE_MISSED = FS_WAKE_MISSED,
  fs_blocks = fs_blocks, fs_set = fs_set, fs_clear = fs_clear,
}
