-- The land-watch duty's read in one call (2026-09-27, Glenn: batch; the
-- Go pass read ws:order and the epoch, then every stream's merging set and
-- its three watch hashes and notes, then the sprint's notes: three round
-- trips before a write). No shebang: the loader prepends the header; this
-- file sorts after 02_card_move.lua and reaches it through NS.
do
  -- ns_land_watch_read() -> rows, flat:
  --   epoch <e>
  --   cfg <slow> <wall> <repos> <notify>            (cfg:land)
  --   sprint <name or ''>                           (the first of sprint:order)
  --   sprintnotes <n> <line>...
  --   stream <name> <m> (<member> <score>)... <s> (<k> <v>)... <g> (<k> <v>)...
  --          <n> (<k> <v>)... <t> <line>...
  --     the merging members with scores under the epoch; land:slow:<s>;
  --     land:merge:<s>; land:merging:<s>; ws:<s>:notes
  local function land_watch_read(keys, args)
    local out = {}
    local function emit(...)
      local n, t = select('#', ...), { ... }
      for i = 1, n do
        local v = t[i]
        if v == nil or v == false then v = '' end
        out[#out + 1] = tostring(v)
      end
    end
    local e = NS.card.epoch()
    emit('epoch', e)
    local cfg = redis.call('HMGET', 'cfg:land', 'slow', 'wall', 'repos', 'notify')
    emit('cfg', cfg[1], cfg[2], cfg[3], cfg[4])
    local first = redis.call('ZRANGE', 'sprint:order', 0, 0)
    local sprint = first[1] or ''
    emit('sprint', sprint)
    local notes = {}
    if sprint ~= '' then notes = redis.call('LRANGE', 'sprint:' .. sprint .. ':notes', 0, -1) end
    emit('sprintnotes', #notes)
    for _, l in ipairs(notes) do emit(l) end
    for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
      local members = redis.call('ZRANGE', NS.card.wskey(e, s, 'merging'), 0, -1, 'WITHSCORES')
      emit('stream', s, #members / 2)
      for _, v in ipairs(members) do emit(v) end
      for _, key in ipairs({ 'land:slow:' .. s, 'land:merge:' .. s, 'land:merging:' .. s }) do
        local h = redis.call('HGETALL', key)
        emit(#h / 2)
        for _, v in ipairs(h) do emit(v) end
      end
      local sn = redis.call('LRANGE', 'ws:' .. s .. ':notes', 0, -1)
      emit(#sn)
      for _, l in ipairs(sn) do emit(l) end
    end
    return out
  end

  redis.register_function{ function_name = 'ns_land_watch_read', callback = land_watch_read, flags = { 'no-writes' } }
end
