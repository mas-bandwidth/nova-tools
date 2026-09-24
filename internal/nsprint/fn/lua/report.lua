-- report-to-read rule of `nova-sprint route` (#2756 section 11 row 5, 3.2
-- row ended(DONE) -> harvested: a card with no commit skips harvest; nova-tools
-- #3036). No shebang: loader.go prepends the single library header. The
-- handler is one Redis Function call that checks the idempotency key
-- `report-to-read:<event id>`, guards the card, writes the one report read
-- task create-only with its queue place and receipt, records the result and
-- XACKs the event, all atomically (spec 2.1 rule 2, 5.4).
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  -- ns_report_read: a card that ended DONE and committed nothing (no pushed
  -- sha, no branch, no PR) -> exactly one read task `report-<label>-a<attempt>`
  -- for a non-author friend at the card's results ref. The id is the label
  -- and attempt, never the reader, so a second end event for the same
  -- attempt finds the task and writes nothing (EXISTS). A reader that is not
  -- registered or is the author refuses with RETRY and writes nothing, so the
  -- event stays pending.
  local function report_read(keys, args)
    local S, group, event_id, label, attempt = args[1], args[2], args[3], args[4], args[5]
    local id, friend, title, repo, ref = args[6], args[7], args[8], args[9], args[10]
    local priority, payload_sha, actor = args[11], args[12], args[13]
    local log = 's:' .. S .. ':log'
    local idem = 's:' .. S .. ':idem'
    local prev = redis.call('HGET', idem, group .. ':' .. event_id)
    if prev then
      redis.call('XACK', log, group, event_id)
      return { 'DUP', prev }
    end
    local card = 's:' .. S .. ':card:' .. label
    local c = redis.call('HMGET', card, 'state', 'outcome', 'attempt', 'pushed_sha', 'branch', 'pr', 'author')
    local pr = c[6] or ''
    local result
    if c[1] ~= 'ended' then
      result = 'SKIP state ' .. tostring(c[1])
    elseif c[2] ~= 'DONE' then
      result = 'SKIP not-done'
    elseif c[3] ~= attempt then
      result = 'SKIP stale-attempt'
    elseif (c[4] or '') ~= '' or (c[5] or '') ~= '' or (pr ~= '' and pr ~= '0') then
      result = 'SKIP committed'
    elseif redis.call('SISMEMBER', 'friends', friend) == 0 then
      return { 'RETRY', 'unregistered ' .. friend }
    elseif c[7] and c[7] ~= '' and c[7] == friend then
      return { 'RETRY', 'author ' .. friend }
    else
      local key = 's:' .. S .. ':task:' .. id
      if redis.call('EXISTS', key) == 1 then
        result = 'EXISTS ' .. id
      else
        local at = now_ms()
        redis.call('HSET', key,
          'kind', 'read', 'repo', repo, 'ref', ref, 'pr', '0', 'head', '',
          'title', title, 'effects', 'none', 'owner', '', 'priority', tostring(priority),
          'state', 'open', 'attempt', '0', 'token', '0', 'payload_sha', payload_sha,
          'reason', '', 'evidence', '', 'claimed_at', '', 'started_at', '',
          'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '')
        redis.call('ZADD', 's:' .. S .. ':open:' .. friend, -tonumber(priority), id)
        redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
        redis.call('XADD', log, '*',
          'kind', 'task push', 'id', id, 'from', '', 'to', 'open',
          'attempt', '0', 'token_sha', '', 'actor', actor or '', 'reason', 'report-to-read',
          'evidence', ref, 'idem', group .. ':' .. event_id, 'at', tostring(at))
        result = 'CREATED ' .. id
      end
    end
    redis.call('HSET', idem, group .. ':' .. event_id, result)
    redis.call('XACK', log, group, event_id)
    return { 'OK', result }
  end

  redis.register_function('ns_report_read', report_read)
end
