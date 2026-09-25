-- Our own CI (nova-tools #3597, #3349; Glenn 2026-09-24 "we can do our own
-- ci"): a request record per head that the benches pull from a pool and run
-- themselves, with one receipt per check and one summary word the lander
-- reads. No shebang: loader.go prepends the one library header; one do-block
-- so these locals never add to the shared chunk's local count.
--
-- Keys (every one under the ~ci:* grant, plus cfg:ci:<repo> and pr:<repo>:<n>):
--   cfg:ci:<repo>            hash: checks (comma list, in order), check:<name> (argv)
--   cfg:ci                   hash: max_attempts (claims per head; default 2, one run
--                            plus one retry for flake)
--   ci:<repo>:<sha>          hash: the request record; ci = pending, green or red
--                            (repo, sha, pr, url, checks, cmd:<name>, requested_at,
--                            attempt, bench, token, lease_until, claimed_at, ended_at;
--                            final = OK or FAIL and why once it ends; last = the word
--                            of an attempt that was retried)
--   ci:<repo>:<sha>:<check>  hash: one receipt per check (rc, wall_ms, log, bench,
--                            attempt, cmd, at, fail = the log's first FAIL line)
--   ci:pool                  zset: <repo>:<sha> scored by the ms it is next claimable
--   ci:nomirror:<bench>      set: the repos whose bench mirror this bench lacks (a
--                            release with nomirror adds; a claim naming the repo in
--                            its mirrors removes); the claim skips their heads
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
--
-- Attempts are capped (2026-09-25: hulk claimed one head 244 times while
-- two benches that cannot clone released it back every tick). A red attempt
-- below cfg:ci max_attempts is re-pooled once more for flake; the attempt at
-- the cap ends it. A claim whose next attempt would pass the cap (the
-- attempts before it were released or lapsed) ends it instead of claiming.
-- Both go through cr_finalise, the one step that writes the verdict: ci red
-- with final FAIL and why = the first failing check and its first FAIL line
-- (else the release reason), out of the pool for good. Only an explicit
-- request --again (ns_ci_request again=1) puts the head back, at attempt 0.
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

  -- The claims one head may take (cfg:ci max_attempts, default 2).
  local function cr_max_attempts()
    local n = tonumber(cr_hget('cfg:ci', 'max_attempts'))
    if not n or n < 1 then return 2 end
    return math.floor(n)
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

  -- The why of a red end: the first check (in request order) whose receipt
  -- is not rc 0, with its first FAIL line; else the release reason; else
  -- the attempts that never wrote a verdict.
  local function cr_why(key, attempt)
    for _, name in ipairs(cr_split(cr_hget(key, 'checks'))) do
      local rc = cr_hget(key .. ':' .. name, 'rc')
      if rc ~= '' and rc ~= '0' then
        local line = cr_hget(key .. ':' .. name, 'fail')
        if line == '' then line = 'rc=' .. rc end
        return name .. ': ' .. line
      end
    end
    local blocked = cr_hget(key, 'blocked')
    if blocked ~= '' then return 'blocked: ' .. blocked end
    return 'no verdict in ' .. tostring(attempt) .. ' attempts (leases lapsed)'
  end

  -- cr_finalise is the one step that writes a verdict: the word and final
  -- on the record, the token cleared (a late receipt is FENCED), the member
  -- out of the pool, and the PR record named by the request takes the word
  -- when its head is this sha.
  local function cr_finalise(key, repo, sha, word, why, now)
    local final = 'OK'
    if word ~= 'green' then final = 'FAIL' end
    redis.call('HSET', key, 'ci', word, 'final', final, 'why', why, 'ended_at', tostring(now),
      'lease_until', '0', 'token', '')
    redis.call('ZREM', 'ci:pool', repo .. ':' .. sha)
    local pr = cr_hget(key, 'pr')
    if pr ~= '' then
      -- The one PR record key, pr:<name>:<n> with the bare repository name
      -- (internal/nsprint/prkey): an owner in repo is stripped (#3740).
      local pr_key = 'pr:' .. (string.match(repo, '([^/]+)$') or repo) .. ':' .. pr
      if cr_hget(pr_key, 'head') == sha then
        redis.call('HSET', pr_key, 'ci', word, 'ci_sha', sha, 'ci_at', tostring(now), 'ci_why', why)
      end
    end
  end

  -- ns_ci_request repo sha pr url wanted again [default_name default_argv]...
  -- -> CREATED pending, EXISTS <ci>, RESET pending, REFUSED <why>.
  -- Create-only: a second request for the same head changes nothing,
  -- whatever it asks for, unless again is 1: then the record goes back to
  -- attempt 0, pending, its receipts and verdict cleared, into the pool now
  -- (the explicit reset after a capped FAIL).
  local function ci_request(keys, args)
    local repo, sha, pr, url, wanted = args[1], args[2], args[3], args[4], args[5]
    -- again sits before the default pairs; a caller from before it sends an
    -- even count after wanted, so the pairs start at 6 for it and at 7 now.
    local again, first = '', 6
    if (#args - 5) % 2 == 1 then again, first = args[6], 7 end
    local key = 'ci:' .. repo .. ':' .. sha
    if redis.call('EXISTS', key) == 1 then
      if again ~= '1' then return { 'EXISTS', cr_hget(key, 'ci') } end
      for _, name in ipairs(cr_split(cr_hget(key, 'checks'))) do
        redis.call('DEL', key .. ':' .. name)
      end
      local at = cr_now_ms()
      redis.call('HSET', key, 'ci', 'pending', 'attempt', '0', 'bench', '', 'token', '', 'lease_until', '0',
        'claimed_at', '0', 'ended_at', '0', 'final', '', 'why', '', 'last', '', 'blocked', '',
        'reset_at', tostring(at))
      redis.call('ZADD', 'ci:pool', at, repo .. ':' .. sha)
      return { 'RESET', 'pending' }
    end
    local names, cmds = cr_declared(repo, args, first)
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

  -- ns_ci_claim bench token lease_ms [mirrors] -> REFUSED role=friends, IDLE [capped], or CLAIMED
  -- field value ... capped <members>. Takes the oldest claimable request
  -- (score <= now). A member whose record is gone or already summarised is
  -- dropped from the pool on the way; one whose next attempt would pass the
  -- cap is ended FAIL (cr_finalise) and named in capped. mirrors is the
  -- comma list of repos the bench holds a mirror of now: each leaves
  -- ci:nomirror:<bench>, and a head of a repo still in that set (with no
  -- request url to clone from instead) is skipped for this bench, left in
  -- the pool for the others: one SISMEMBER per candidate.
  -- A bench whose registry role is friends (#3634: "Studio is for friends.
  -- Fleet is for CI and swarms.") claims nothing: REFUSED role=friends, no
  -- write.
  local function ci_claim(keys, args)
    local bench, token, lease_ms = args[1], args[2], tonumber(args[3])
    if cr_hget('bench:' .. bench .. ':desired', 'role') == 'friends' then
      return { 'REFUSED', 'role=friends' }
    end
    local nomirror = 'ci:nomirror:' .. bench
    for _, repo in ipairs(cr_split(args[4] or '')) do
      redis.call('SREM', nomirror, repo)
    end
    local now = cr_now_ms()
    local max_attempts = cr_max_attempts()
    local capped = {}
    local cands = redis.call('ZRANGEBYSCORE', 'ci:pool', '-inf', tostring(now), 'LIMIT', '0', '16')
    for _, member in ipairs(cands) do
      local key = 'ci:' .. member
      local state = cr_hget(key, 'ci')
      local attempt = (tonumber(cr_hget(key, 'attempt')) or 0) + 1
      if state == 'pending' and cr_hget(key, 'url') == ''
        and redis.call('SISMEMBER', nomirror, cr_hget(key, 'repo')) == 1 then
        -- this bench cannot stage the repo; another bench takes the head
      elseif state == 'pending' and attempt > max_attempts then
        cr_finalise(key, cr_hget(key, 'repo'), cr_hget(key, 'sha'), 'red', cr_why(key, attempt - 1), now)
        capped[#capped + 1] = member
      elseif state == 'pending' then
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
        reply[#reply + 1] = 'capped'
        reply[#reply + 1] = cr_join(capped)
        return reply
      else
        redis.call('ZREM', 'ci:pool', member)
      end
    end
    return { 'IDLE', cr_join(capped) }
  end

  -- ns_ci_receipt repo sha check token rc wall_ms log bench lease_ms [fail]
  -- -> RECEIPT <pending, retry, green or red> <done>/<total>; FENCED,
  -- NOTFOUND or REFUSED <why> write nothing. Writes the check's receipt
  -- (fail is the log's first FAIL line); when every check of the record has
  -- one the attempt is green (every rc 0) or red. Green, or red at the
  -- attempt cap, is the verdict (cr_finalise); red below the cap is retry:
  -- the record stays pending with last=red and goes back to the pool now.
  local function ci_receipt(keys, args)
    local repo, sha, check, token = args[1], args[2], args[3], args[4]
    local rc, wall_ms, log, bench, lease_ms = args[5], args[6], args[7], args[8], tonumber(args[9])
    local fail = args[10] or ''
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
      'bench', bench, 'attempt', attempt, 'cmd', cr_hget(key, 'cmd:' .. check), 'at', tostring(now), 'fail', fail)
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
    local tally = tostring(done) .. '/' .. tostring(#checks)
    if not red then
      cr_finalise(key, repo, sha, 'green', '', now)
      return { 'RECEIPT', 'green', tally }
    end
    if (tonumber(attempt) or 0) < cr_max_attempts() then
      redis.call('HSET', key, 'last', 'red', 'bench', '', 'token', '', 'lease_until', '0')
      redis.call('ZADD', 'ci:pool', now, member)
      return { 'RECEIPT', 'retry', tally }
    end
    cr_finalise(key, repo, sha, 'red', cr_why(key, attempt), now)
    return { 'RECEIPT', 'red', tally }
  end

  -- ns_ci_release repo sha token reason [nomirror] -> RELEASED, FENCED or
  -- NOTFOUND. A bench that could not run the checks (the clone failed) has
  -- no evidence about the head: the request goes back to the pool now, for
  -- another bench, with the reason on the record. Attempts are counted here
  -- and capped at the next claim (ci_claim). nomirror 1 is a bench defect
  -- (the bench holds no mirror of the repo), not a try at the head: the
  -- attempt is given back and the bench goes into ci:nomirror:<bench> for
  -- the repo, so its claims skip the repo until its mirror exists.
  local function ci_release(keys, args)
    local repo, sha, token, reason = args[1], args[2], args[3], args[4]
    local key = 'ci:' .. repo .. ':' .. sha
    if redis.call('EXISTS', key) == 0 then return { 'NOTFOUND' } end
    if token == '' or cr_hget(key, 'token') ~= token then return { 'FENCED' } end
    local now = cr_now_ms()
    if args[5] == '1' then
      local attempt = (tonumber(cr_hget(key, 'attempt')) or 1) - 1
      if attempt < 0 then attempt = 0 end
      redis.call('SADD', 'ci:nomirror:' .. cr_hget(key, 'bench'), repo)
      redis.call('HSET', key, 'attempt', tostring(attempt))
    end
    redis.call('HSET', key, 'bench', '', 'token', '', 'lease_until', '0', 'blocked', reason)
    redis.call('ZADD', 'ci:pool', now, repo .. ':' .. sha)
    return { 'RELEASED' }
  end

  redis.register_function('ns_ci_request', ci_request)
  redis.register_function('ns_ci_claim', ci_claim)
  redis.register_function('ns_ci_receipt', ci_receipt)
  redis.register_function('ns_ci_release', ci_release)
end
