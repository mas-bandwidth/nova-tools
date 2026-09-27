-- The land-watch duty's pass in one call (2026-09-27, Glenn: batch; Stella's
-- read of nova-tools #4449: an active tick with one merging member and an
-- open merge card took three trips: the read, the member and card reads,
-- the writes). The mechanical half of the pass lives here: the first-sight
-- stamps, the stamps and noted words of members that left, the empty
-- episode's cleanup (the card fields, the slow record, the stamps, the
-- card's claim), and the slow record itself. Go keeps what needs the world:
-- the lines, the note (rare: one per member and word per stay), the merge
-- and escalation cards.
--
-- Checked against rowan-new specs/tla/LandWatch.tla (findings L1 and L2):
-- the note's episode is the member's stay, keyed on the watch's own first
-- sight, never on the move's stamp, which can arrive later and older; and
-- the words noted follow the member (land:noted:<stream>, id -> words),
-- never the stream's one record, which comes and goes with the age.
--
-- No shebang: the loader prepends the header; this file sorts after
-- 02_card_move.lua and reaches it through NS.
do
  local DEFAULT_SLOW_MS, DEFAULT_WALL_MS = 600000, 1800000

  local function str(v)
    if v == nil or v == false then return '' end
    return tostring(v)
  end

  local function fields(s)
    local out = {}
    for w in string.gmatch(str(s), '%S+') do out[#out + 1] = w end
    return out
  end

  local function hgetall(key)
    local h = redis.call('HGETALL', key)
    local rec = {}
    for i = 1, #h, 2 do rec[h[i]] = h[i + 1] end
    return rec
  end

  -- has_word(words, w): w is in the comma-joined words.
  local function has_word(words, w)
    for _, x in ipairs(fields((string.gsub(str(words), ',', ' ')))) do
      if x == w then return true end
    end
    return false
  end

  -- ms(v): v as epoch milliseconds, or nil.
  local function ms(v)
    local s = str(v)
    if string.match(s, '^%s*%d+%s*$') then return tonumber(s) end
    return nil
  end

  -- members_of(e, s, now, write): the merging members of stream s under
  -- epoch e in score order, each with its at (the move's merging_at when the
  -- record has one, else the watch's first sight, stamped now when write),
  -- pr and paths. When write, the stamps and noted words of members that
  -- left go.
  local function members_of(e, s, now, write)
    local mk = 'land:merging:' .. s
    local z = redis.call('ZRANGE', NS.card.wskey(e, s, 'merging'), 0, -1, 'WITHSCORES')
    local out, present = {}, {}
    for i = 1, #z, 2 do
      local id = z[i]
      present[id] = true
      if write then redis.call('HSETNX', mk, id, now) end
      local first = ms(redis.call('HGET', mk, id)) or now
      local v = redis.call('HMGET', 'task:' .. id, 'merging_at', 'pr', 'paths')
      local at = ms(v[1]) or first
      out[#out + 1] = { id = id, score = z[i + 1], at = at, first = first, pr = str(v[2]), paths = str(v[3]) }
    end
    if write then
      local left = {}
      for _, id in ipairs(redis.call('HKEYS', mk)) do
        if not present[id] then left[#left + 1] = id end
      end
      if #left > 0 then
        redis.call('HDEL', mk, unpack(left))
        redis.call('HDEL', 'land:noted:' .. s, unpack(left))
      end
    end
    return out
  end

  -- clean(s, merge): the end of an episode: the card fields go (seq stays
  -- so the next id is new), the closed card's claim goes with it (compare
  -- and delete: a claim another writer took since is kept), the slow
  -- record, the stamps and the noted words go.
  local function clean(s, merge)
    local mk = 'land:merge:' .. s
    if merge.task ~= nil or merge.escalation ~= nil or merge.to ~= nil then
      redis.call('HDEL', mk, 'task', 'to', 'cut_at', 'members', 'escalated', 'escalation', 'after')
    end
    if merge.task and merge.task ~= '' then redis.call('DEL', 'land:brief:' .. merge.task) end
    if merge.owner and string.sub(merge.owner, 1, 5) == 'card:' then
      if redis.call('HGET', mk, 'owner') == merge.owner then
        redis.call('HDEL', mk, 'owner', 'owner_until', 'owner_at')
      end
    end
    redis.call('DEL', 'land:slow:' .. s, 'land:merging:' .. s, 'land:noted:' .. s)
  end

  local function emit_into(out)
    return function(...)
      local n, t = select('#', ...), { ... }
      for i = 1, n do
        local v = t[i]
        if v == nil or v == false then v = '' end
        out[#out + 1] = tostring(v)
      end
    end
  end

  -- pass(keys, args): args now_ms, slow_ms (or '' for cfg:land), wall_ms
  -- (or '').
  local function pass(keys, args)
    local now = tonumber(args[1]) or 0
    local out = {}
    local emit = emit_into(out)
    local e = NS.card.epoch()
    emit('epoch', e)
    local cfg = redis.call('HMGET', 'cfg:land', 'slow', 'wall', 'repos', 'notify')
    local slow = tonumber(args[2])
    if not slow or slow <= 0 then
      local c = tonumber(str(cfg[1]))
      slow = (c and c > 0) and c * 1000 or DEFAULT_SLOW_MS
    end
    local wall = tonumber(args[3])
    if not wall or wall <= 0 then
      local c = tonumber(str(cfg[2]))
      wall = (c and c > 0) and c * 1000 or DEFAULT_WALL_MS
    end
    emit('cfg', slow, wall, cfg[3], cfg[4])
    local first = redis.call('ZRANGE', 'sprint:order', 0, 0)
    local sprint = first[1] or ''
    emit('sprint', sprint)
    local snotes = {}
    if sprint ~= '' then snotes = redis.call('LRANGE', 'sprint:' .. sprint .. ':notes', 0, -1) end
    emit('sprintnotes', #snotes)
    for _, l in ipairs(snotes) do emit(l) end
    for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
      local members = members_of(e, s, now, true)
      local merge = hgetall('land:merge:' .. s)
      if #members == 0 then
        clean(s, merge)
        emit('stream', s, 0)
      else
        emit('stream', s, #members)
        for _, m in ipairs(members) do emit('member', m.id, m.score, m.at, m.pr, m.paths) end
        local mh = redis.call('HGETALL', 'land:merge:' .. s)
        emit('merge', #mh / 2)
        for _, v in ipairs(mh) do emit(v) end
        if merge.task and merge.task ~= '' then
          local v = redis.call('HMGET', 'task:' .. merge.task, 'state', 'reason', 'dest')
          emit('card', merge.task, v[1], v[2], v[3])
        end
        if merge.escalation and merge.escalation ~= '' then
          local v = redis.call('HMGET', 'task:' .. merge.escalation, 'state')
          emit('escalation', merge.escalation, v[1])
        end
        for _, sid in ipairs(fields(merge.after)) do
          local v = redis.call('HMGET', 'task:' .. sid, 'state', 'where')
          emit('stop', sid, v[1], v[2])
        end
        local sn = redis.call('LRANGE', 'ws:' .. s .. ':notes', 0, -1)
        emit('notes', #sn)
        for _, l in ipairs(sn) do emit(l) end
        -- the oldest by merging_at, the work order breaking ties
        local oldest = members[1]
        for i = 2, #members do
          if members[i].at < oldest.at then oldest = members[i] end
        end
        local age = now - oldest.at
        local sk = 'land:slow:' .. s
        if age < slow then
          redis.call('DEL', sk)
        else
          local word, max, stalled = 'LAND-SLOW', slow, '0'
          if age >= wall then word, max, stalled = 'LAND-WALL', wall, '1' end
          local old = redis.call('HMGET', sk, 'word', 'oldest')
          local changed = (str(old[1]) ~= word or str(old[2]) ~= oldest.id) and 1 or 0
          redis.call('HSET', sk, 'word', word, 'oldest', oldest.id, 'oldest_at', oldest.at,
            'age_ms', age, 'stalled', stalled, 'at', now)
          local noted = redis.call('HGET', 'land:noted:' .. s, oldest.id)
          local note = has_word(noted, word) and 0 or 1
          emit('slow', word, oldest.id, oldest.at, age, changed, note, max, noted)
        end
      end
    end
    return out
  end

  -- ns_land_brief_read(stream, now_ms) -> the rows a merge card's brief is
  -- rendered from, written nowhere: cfg <repos>, sprint <name>, sprintnotes
  -- <n> <line>..., member <id> <score> <at_ms> <pr> <paths>..., notes <n>
  -- <line>... (a card's brief is a view of the stream since #4449: the
  -- watch writes no text every second; card render renders it).
  local function brief_read(keys, args)
    local s, now = str(args[1]), tonumber(args[2]) or 0
    local out = {}
    local emit = emit_into(out)
    local e = NS.card.epoch()
    local cfg = redis.call('HMGET', 'cfg:land', 'repos')
    emit('cfg', cfg[1])
    local first = redis.call('ZRANGE', 'sprint:order', 0, 0)
    local sprint = first[1] or ''
    emit('sprint', sprint)
    local snotes = {}
    if sprint ~= '' then snotes = redis.call('LRANGE', 'sprint:' .. sprint .. ':notes', 0, -1) end
    emit('sprintnotes', #snotes)
    for _, l in ipairs(snotes) do emit(l) end
    for _, m in ipairs(members_of(e, s, now, false)) do emit('member', m.id, m.score, m.at, m.pr, m.paths) end
    local sn = redis.call('LRANGE', 'ws:' .. s .. ':notes', 0, -1)
    emit('notes', #sn)
    for _, l in ipairs(sn) do emit(l) end
    return out
  end

  redis.register_function('ns_land_watch_pass', pass)
  redis.register_function{ function_name = 'ns_land_brief_read', callback = brief_read, flags = { 'no-writes' } }
end
