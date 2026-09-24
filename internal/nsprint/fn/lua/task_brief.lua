-- Brief record for nova-tools#3154 (rev 6). No shebang: the loader
-- (internal/nsprint/fn/loader.go) prepends the single library header. The four
-- brief fields on a task hash (brief_tmpl_sha, brief_sha, brief_src, brief_at)
-- have ONE writer, this Function. Its guard follows task_beat.lua: check the
-- guard, then write, in one atomic call.
do
  -- task_brief: KEYS[1] is s:<sprint>:task:<id>; ARGV is the exact title and
  -- kind render read in its first round trip, then brief_tmpl_sha and
  -- brief_sha. MISSING and STALE write nothing, so render never creates a task
  -- and never records a digest of fields that changed between its two round
  -- trips. brief_src is derived here from the fields the guard just verified,
  -- so no caller can pass a digest of other fields. It is a change detector,
  -- not a security boundary; sha1 is the digest Redis Lua offers.
  local function task_brief(keys, args)
    local key = keys[1]
    local title, kind, tmpl_sha, brief_sha = args[1], args[2], args[3], args[4]
    if redis.call('EXISTS', key) == 0 then
      return { 'MISSING' }
    end
    if redis.call('HGET', key, 'title') ~= title or redis.call('HGET', key, 'kind') ~= kind then
      return { 'STALE' }
    end
    local src = redis.sha1hex(kind .. '\0' .. title)
    local t = redis.call('TIME')
    local at = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
    redis.call('HSET', key, 'brief_tmpl_sha', tmpl_sha, 'brief_sha', brief_sha,
      'brief_src', src, 'brief_at', tostring(at))
    return { 'OK', src }
  end

  redis.register_function('ns_task_brief', task_brief)
end
