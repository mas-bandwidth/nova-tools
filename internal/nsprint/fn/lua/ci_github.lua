-- The GitHub leg of CI results in Redis (nova-tools #3597): a check_run or
-- workflow_run delivery that the webhook receiver appended to ev:github
-- (nova-post hook, #2657) becomes one field of ci:<repo>:<sha>:gh, and the
-- summary word on that hash is refolded in the same call. Nothing polls
-- GitHub for a check state: readers (ci status, the dev-red duty) read this
-- hash. No shebang: loader.go prepends the one library header.
--
-- Keys:
--   ci:<repo>:<sha>:gh  hash, written only here (one writer):
--     check:<name>  '<word> <check_run_id> <at>' of the newest attempt of that check
--     wf:<name>     '<word> <run_id> <at>' of the newest run of that workflow
--     gh            the fold: red if any is red, pending if any is pending, else green
--     gh_fail       the first red name in byte order (kind:name), empty when none is red
--     gh_at         the newest event time folded in; n the number of named runs
--     pr            the first pull request number any event named
--     ev_id         the ev:github entry last applied; at the Redis ms of that write
--   ev:github           the stream; the entry is XACKed for the group in this call
--
-- word is green (success, skipped, neutral), red (failure, timed_out,
-- cancelled, action_required, startup_failure) or pending (anything not
-- completed, and a completed run with no conclusion we name).
--
-- The request record ci:<repo>:<sha> of our own CI (ci_run.lua) is create-only
-- by EXISTS, so the GitHub side never writes it: a webhook that arrived first
-- would turn the request into EXISTS and the head would never be pooled. The
-- suffix gh is reserved against check names in Go (reservedSuffixes).
do
  local function cg_word(status, conclusion)
    if status ~= 'completed' then return 'pending' end
    if conclusion == 'success' or conclusion == 'skipped' or conclusion == 'neutral' then
      return 'green'
    end
    if conclusion == 'failure' or conclusion == 'timed_out' or conclusion == 'cancelled'
      or conclusion == 'action_required' or conclusion == 'startup_failure' then
      return 'red'
    end
    return 'pending'
  end

  -- Decimal ids beyond 2^53 compare exactly as strings: longer is larger.
  local function cg_cmp_id(a, b)
    if #a ~= #b then return #a < #b and -1 or 1 end
    if a == b then return 0 end
    return a < b and -1 or 1
  end

  local function cg_rank(word)
    if word == 'pending' then return 0 end
    return 1
  end

  -- newer: the entry replaces the stored attempt when its id is larger, or
  -- the id is the same and its time is later, or the same time with a word at
  -- least as final (a redelivery rewrites the same value).
  local function cg_newer(cur, id, at, word)
    if not cur then return true end
    local cword, cid, cat = string.match(cur, '^(%S+) (%S*) (%S*)$')
    if not cword then return true end
    local c = cg_cmp_id(id, cid)
    if c ~= 0 then return c > 0 end
    if at ~= cat then return at > cat end
    return cg_rank(word) >= cg_rank(cword)
  end

  -- ns_ci_github KEYS[1]=ci:<repo>:<sha>:gh KEYS[2]=ev:github
  -- ARGV group entry_id kind name run_id status conclusion at pr
  -- -> { APPLIED|KEPT, gh, gh_fail }. kind is check_run or workflow_run.
  local function ci_github(keys, args)
    local key, stream = keys[1] or '', keys[2] or ''
    local group, entry, kind, name = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
    local id, status, conclusion, at, pr = args[5] or '', args[6] or '', args[7] or '', args[8] or '', args[9] or ''
    if not string.match(key, '^ci:[^:]+:[0-9a-f]+:gh$') then
      return redis.error_reply('ERR ns_ci_github: key is not ci:<repo>:<sha>:gh')
    end
    if stream == '' or group == '' or entry == '' then
      return redis.error_reply('ERR ns_ci_github: usage stream group entry_id')
    end
    local prefix = nil
    if kind == 'check_run' then prefix = 'check:' elseif kind == 'workflow_run' then prefix = 'wf:' end
    if not prefix or name == '' or string.find(name, '%s') or not string.match(id, '^[0-9]+$')
      or string.find(at, '%s') then
      return redis.error_reply('ERR ns_ci_github: want check_run|workflow_run, a name, a decimal id and an at without spaces')
    end
    local word = cg_word(status, conclusion)
    local field = prefix .. name
    local verdict = 'KEPT'
    if cg_newer(redis.call('HGET', key, field), id, at, word) then
      verdict = 'APPLIED'
      redis.call('HSET', key, field, word .. ' ' .. id .. ' ' .. at)
      local all = redis.call('HGETALL', key)
      local fold, fail, n, newest = 'green', '', 0, at
      for i = 1, #all, 2 do
        local f, v = all[i], all[i + 1]
        local kname = string.match(f, '^check:(.+)$') or string.match(f, '^wf:(.+)$')
        if kname then
          local w, _, a = string.match(v, '^(%S+) (%S*) (%S*)$')
          n = n + 1
          if w == 'red' then
            fold = 'red'
            if fail == '' or f < fail then fail = f end
          elseif w ~= 'green' and fold ~= 'red' then
            fold = 'pending'
          end
          if a and a > newest then newest = a end
        end
      end
      local t = redis.call('TIME')
      local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
      redis.call('HSET', key, 'gh', fold, 'gh_fail', fail, 'gh_at', newest, 'n', tostring(n),
        'ev_id', entry, 'at', tostring(now))
      if pr ~= '' and not redis.call('HGET', key, 'pr') then
        redis.call('HSET', key, 'pr', pr)
      end
    end
    redis.call('XACK', stream, group, entry)
    local cur = redis.call('HMGET', key, 'gh', 'gh_fail')
    return { verdict, cur[1] or '', cur[2] or '' }
  end

  redis.register_function('ns_ci_github', ci_github)
end
