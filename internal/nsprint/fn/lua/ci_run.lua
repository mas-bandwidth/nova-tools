-- Our own CI (nova-tools #3597, #3349; Glenn 2026-09-24 "we can do our own
-- ci"): a request record per head that the benches pull from a pool and run
-- themselves, with one receipt per check and one summary word the lander
-- reads. No shebang: loader.go prepends the one library header; one do-block
-- so these locals never add to the shared chunk's local count.
--
-- Keys (every one under the ~ci:* grant, plus cfg:ci:<repo> and pr:<repo>:<n>):
--   cfg:ci:<repo>            hash: checks (comma list, in order), check:<name> (argv)
--   ci:<repo>:<sha>          hash: the request record; ci = pending, green or red
--                            (repo, sha, pr, url, checks, cmd:<name>, requested_at,
--                            attempt, bench, token, lease_until, claimed_at, ended_at)
--   ci:<repo>:<sha>:<check>  hash: one receipt per check (rc, wall_ms, log, bench,
--                            attempt, cmd, at)
--   ci:pool                  zset: <repo>:<sha> scored by the ms it is next claimable
--   pr:<repo>:<n>            hash: ci, ci_sha, ci_at are written when its head is the sha
--
-- The record key is the one the ci card verdict never used (ci.lua writes
-- ci:<repo>:<head>:<gid>, :gids, :waiting; runner rows live under :runners),
-- and a check name may not be any of those suffixes (Go refuses them), so the
-- two families share the prefix and never a key.
--
-- Runner-only (#3349): nothing here reads or writes bench:<b>:desired legs.
-- A bench that calls ns_ci_claim carries the checks; the receipt says which.
--
-- The lease is the pool score: a claim moves the member to now+lease_ms, so a
-- second bench sees it only after the lease lapses, and every receipt moves
-- it again (the receipt is the beat). A receipt whose token is not the
-- record's is FENCED and writes nothing, so a lapsed bench that finishes late
-- cannot overwrite the live attempt.
do
  local function cr_now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function cr_hget(key, field)
    local v = redis.call('HGET', key, field)
    if not v then return '' end
    return tostring(v)
  end

  -- 'a,b,c' -> {'a','b','c'}; empty items dropped.
  local function cr_split(s)
    local out = {}
    for item in string.gmatch(s or '', '[^,]+') do
      if item ~= '' then out[#out + 1] = item end
    end
    return out
  end

  local function cr_join(list)
    local s = ''
    for i, v in ipairs(list) do
      if i > 1 then s = s .. ',' end
      s = s .. v
    end
    return s
  end

  -- The declared set of a repo: cfg:ci:<repo> when it names checks, else the
  -- default pairs the caller passed (name, argv, name, argv, ...).
  local function cr_declared(repo, args, first)
    local names, cmds = {}, {}
    local cfg_checks = cr_hget('cfg:ci:' .. repo, 'checks')
    if cfg_checks ~= '' then
      for _, name in ipairs(cr_split(cfg_checks)) do
        local cmd = cr_hget('cfg:ci:' .. repo, 'check:' .. name)
        if cmd == '' then return nil, 'cfg:ci:' .. repo .. ' names check ' .. name .. ' with no check:' .. name .. ' argv' end
        names[#names + 1] = name
        cmds[name] = cmd
      end
      return names, cmds
    end
    local i = first
    while i + 1 <= #args do
      names[#names + 1] = args[i]
      cmds[args[i]] = args[i + 1]
      i = i + 2
    end
    return names, cmds
  end

  -- ns_ci_request repo sha pr url wanted [default_name default_argv]...
  -- -> CREATED pending, EXISTS <ci>, REFUSED <why>. Create-only: a second
  -- request for the same head changes nothing, whatever it asks for.
  local function ci_request(keys, args)
    local repo, sha, pr, url, wanted = args[1], args[2], args[3], args[4], args[5]
    local key = 'ci:' .. repo .. ':' .. sha
    if redis.call('EXISTS', key) == 1 then return { 'EXISTS', cr_hget(key, 'ci') } end
    local names, cmds = cr_declared(repo, args, 6)
    if not names then return { 'REFUSED', cmds } end
    if #names == 0 then return { 'REFUSED', 'no checks declared for ' .. repo .. '; write cfg:ci:' .. repo } end
    local chosen = names
    if wanted ~= '' then
      chosen = {}
      for _, w in ipairs(cr_split(wanted)) do
        if not cmds[w] then return { 'REFUSED', 'check ' .. w .. ' is not declared for ' .. repo .. '; declared: ' .. cr_join(names) } end
        chosen[#chosen + 1] = w
      end
    end
    local at = cr_now_ms()
    redis.call('HSET', key, 'repo', repo, 'sha', sha, 'pr', pr, 'url', url,
      'checks', cr_join(chosen), 'requested_at', tostring(at), 'ci', 'pending',
      'attempt', '0', 'bench', '', 'token', '', 'lease_until', '0', 'claimed_at', '0', 'ended_at', '0')
    for _, name in ipairs(chosen) do
      redis.call('HSET', key, 'cmd:' .. name, cmds[name])
    end
    redis.call('ZADD', 'ci:pool', at, repo .. ':' .. sha)
    return { 'CREATED', 'pending' }
  end

  -- ns_ci_claim bench token lease_ms -> IDLE, or CLAIMED field value ...
  -- Takes the oldest claimable request (score <= now). A member whose record
  -- is gone or already summarised is dropped from the pool on the way.
  local function ci_claim(keys, args)
    local bench, token, lease_ms = args[1], args[2], tonumber(args[3])
    local now = cr_now_ms()
    local cands = redis.call('ZRANGEBYSCORE', 'ci:pool', '-inf', tostring(now), 'LIMIT', '0', '16')
    for _, member in ipairs(cands) do
      local key = 'ci:' .. member
      local state = cr_hget(key, 'ci')
      if state == 'pending' then
        local attempt = tonumber(cr_hget(key, 'attempt')) + 1
        local checks = cr_split(cr_hget(key, 'checks'))
        for _, name in ipairs(checks) do
          redis.call('DEL', key .. ':' .. name)
        end
        redis.call('HSET', key, 'attempt', tostring(attempt), 'bench', bench, 'token', token,
          'lease_until', tostring(now + lease_ms), 'claimed_at', tostring(now), 'blocked', '')
        redis.call('ZADD', 'ci:pool', now + lease_ms, member)
        local reply = { 'CLAIMED' }
        local rec = redis.call('HGETALL', key)
        for _, v in ipairs(rec) do reply[#reply + 1] = v end
        return reply
      end
      redis.call('ZREM', 'ci:pool', member)
    end
    return { 'IDLE' }
  end

  -- ns_ci_receipt repo sha check token rc wall_ms log bench lease_ms
  -- -> RECEIPT <pending, green or red> <done>/<total>; FENCED, NOTFOUND or
  -- REFUSED <why> write nothing. Writes the check's receipt; when every check
  -- of the record has one the summary is green (every rc 0) or red, the
  -- member leaves the pool and the PR record named by the request takes the
  -- word when its head is this sha.
  local function ci_receipt(keys, args)
    local repo, sha, check, token = args[1], args[2], args[3], args[4]
    local rc, wall_ms, log, bench, lease_ms = args[5], args[6], args[7], args[8], tonumber(args[9])
    local key = 'ci:' .. repo .. ':' .. sha
    if redis.call('EXISTS', key) == 0 then return { 'NOTFOUND' } end
    if token == '' or cr_hget(key, 'token') ~= token then return { 'FENCED' } end
    local checks = cr_split(cr_hget(key, 'checks'))
    local named = false
    for _, name in ipairs(checks) do
      if name == check then named = true end
    end
    if not named then return { 'REFUSED', 'check ' .. check .. ' is not in the request' } end
    local now = cr_now_ms()
    local attempt = cr_hget(key, 'attempt')
    redis.call('HSET', key .. ':' .. check, 'rc', rc, 'wall_ms', wall_ms, 'log', log,
      'bench', bench, 'attempt', attempt, 'cmd', cr_hget(key, 'cmd:' .. check), 'at', tostring(now))
    local done, red = 0, false
    for _, name in ipairs(checks) do
      local got = cr_hget(key .. ':' .. name, 'rc')
      if got ~= '' then
        done = done + 1
        if got ~= '0' then red = true end
      end
    end
    local member = repo .. ':' .. sha
    if done < #checks then
      redis.call('HSET', key, 'lease_until', tostring(now + lease_ms))
      redis.call('ZADD', 'ci:pool', now + lease_ms, member)
      return { 'RECEIPT', 'pending', tostring(done) .. '/' .. tostring(#checks) }
    end
    local word = 'green'
    if red then word = 'red' end
    redis.call('HSET', key, 'ci', word, 'ended_at', tostring(now), 'lease_until', '0')
    redis.call('ZREM', 'ci:pool', member)
    local pr = cr_hget(key, 'pr')
    if pr ~= '' then
      local pr_key = 'pr:' .. repo .. ':' .. pr
      if cr_hget(pr_key, 'head') == sha then
        redis.call('HSET', pr_key, 'ci', word, 'ci_sha', sha, 'ci_at', tostring(now))
      end
    end
    return { 'RECEIPT', word, tostring(done) .. '/' .. tostring(#checks) }
  end

  -- ns_ci_release repo sha token reason -> RELEASED, FENCED or NOTFOUND.
  -- A bench that could not run the checks (the clone failed) has no evidence
  -- about the head: the request goes back to the pool now, for another bench,
  -- with the reason on the record. Attempts are counted, never capped here.
  local function ci_release(keys, args)
    local repo, sha, token, reason = args[1], args[2], args[3], args[4]
    local key = 'ci:' .. repo .. ':' .. sha
    if redis.call('EXISTS', key) == 0 then return { 'NOTFOUND' } end
    if token == '' or cr_hget(key, 'token') ~= token then return { 'FENCED' } end
    local now = cr_now_ms()
    redis.call('HSET', key, 'bench', '', 'token', '', 'lease_until', '0', 'blocked', reason)
    redis.call('ZADD', 'ci:pool', now, repo .. ':' .. sha)
    return { 'RELEASED' }
  end

  redis.register_function('ns_ci_request', ci_request)
  redis.register_function('ns_ci_claim', ci_claim)
  redis.register_function('ns_ci_receipt', ci_receipt)
  redis.register_function('ns_ci_release', ci_release)
end
