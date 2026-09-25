-- The ingest consumer of ev:github (nova-tools #2657): the one inbound path
-- from GitHub into OUR records besides import. A pull_request delivery the
-- signed receiver (nova-post hook) appended updates the PR record's head and
-- state; an issues closed delivery lands the issue's cards only when our
-- lander closed it, and is a finding otherwise. Every entry is one call that
-- writes, counts and XACKs, so the ack never runs ahead of the effect, and
-- nothing here or in the Go loop (internal/nsprint/ghingest) asks GitHub.
-- No shebang: loader.go prepends the one library header.
--
-- Keys:
--   pr:<name>:<n>            the PR record (internal/nsprint/prkey; the
--                            stream lander creates it). This call writes
--                            only head (with ci=pending and mergeable='' on
--                            a new head, as the record op does), state,
--                            merge_sha, gh_state, gh_at, ev_id, updated_at,
--                            and only when the record exists: a PR with no
--                            record is not ours and is acked as UNKNOWN.
--   issue:<name>:<n>:cards   the issue's cards (02_card_move.lua writes it
--                            at card create; ns_gh_issue_index backfills).
--   ev:github                the stream; the entry is XACKed for the group.
--   gh:findings              stream: an issue closed by anyone but the
--                            lander, or closed not as completed, or closed
--                            with no card to land; the card never moves.
--   gh:ingest                hash: applied kept unknown landed findings
--                            counters, last_id, at.
do
  local CARD = NS.card

  local function gh_now()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function gh_count(field, entry)
    redis.call('HINCRBY', 'gh:ingest', field, 1)
    redis.call('HSET', 'gh:ingest', 'last_id', entry, 'at', tostring(gh_now()))
  end

  -- The PR record's state after one pull_request action. GitHub's word
  -- (open, closed, merged) never overwrites the lander's own terminal words:
  -- a member the stream lander marked landed is closed on GitHub unmerged by
  -- design, and a merged PR stays merged.
  local function gh_pr_state(cur, action, merged)
    if action == 'closed' then
      if cur == 'landed' or cur == 'merged' then return cur end
      if merged == '1' then return 'merged' end
      return 'closed'
    end
    if action == 'reopened' and cur == 'closed' then return 'open' end
    if action == 'opened' and (cur == nil or cur == '') then return 'open' end
    return cur
  end

  -- KEYS[1]=pr:<name>:<n> KEYS[2]=ev:github
  -- ARGV group entry action head at gh_state merged merge_sha
  -- -> { APPLIED|KEPT|UNKNOWN, state, head }
  local function gh_pr(keys, args)
    local key, stream = keys[1] or '', keys[2] or ''
    local group, entry, action, head = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
    local at, ghs, merged, msha = args[5] or '', args[6] or '', args[7] or '', args[8] or ''
    if not string.match(key, '^pr:[^:]+:[0-9]+$') or stream == '' or group == '' or entry == '' then
      return redis.error_reply('ERR ns_gh_pr: want pr:<name>:<n>, a stream, a group and an entry id')
    end
    if redis.call('EXISTS', key) == 0 then
      redis.call('XACK', stream, group, entry)
      gh_count('unknown', entry)
      return { 'UNKNOWN', '', '' }
    end
    local cur = redis.call('HMGET', key, 'head', 'state', 'gh_at', 'gh_state')
    if at ~= '' and cur[3] and cur[3] ~= '' and at < cur[3] then
      redis.call('XACK', stream, group, entry)
      gh_count('kept', entry)
      return { 'KEPT', cur[2] or '', cur[1] or '' }
    end
    local word = ghs
    if merged == '1' then word = 'merged' end
    local h = {}
    local function put(f, v)
      h[#h + 1] = f
      h[#h + 1] = v
    end
    if head ~= '' and head ~= (cur[1] or '') then
      put('head', head)
      if (cur[1] or '') ~= '' then
        put('ci', 'pending')
        put('mergeable', '')
      end
    end
    local state = gh_pr_state(cur[2], action, merged)
    if state and state ~= cur[2] then put('state', state) end
    if merged == '1' and msha ~= '' then put('merge_sha', msha) end
    if word ~= '' and word ~= cur[4] then put('gh_state', word) end
    local verdict = 'KEPT'
    if #h > 0 then
      verdict = 'APPLIED'
      put('gh_at', at)
      put('ev_id', entry)
      put('updated_at', tostring(gh_now()))
      redis.call('HSET', key, unpack(h))
    end
    redis.call('XACK', stream, group, entry)
    gh_count(verdict == 'APPLIED' and 'applied' or 'kept', entry)
    local now = redis.call('HMGET', key, 'state', 'head')
    return { verdict, now[1] or '', now[2] or '' }
  end

  local function gh_finding(entry, repo, n, sender, reason, why, cards, at)
    redis.call('XADD', 'gh:findings', 'MAXLEN', '~', '10000', '*',
      'kind', 'issue-closed', 'repo', repo, 'n', n, 'sender', sender, 'reason', reason,
      'why', why, 'cards', tostring(cards), 'ev_id', entry, 'at', at)
    gh_count('findings', entry)
  end

  -- KEYS[1]=issue:<name>:<n>:cards KEYS[2]=ev:github
  -- ARGV group entry repo n sender state_reason at landers (space-separated)
  -- -> { LANDED, landed, already, refused } or { FINDING, reason, cards }.
  -- Lands every card of the issue that is not done/fail, through the one
  -- move (state landed, done/ok), only when sender is one of our landers
  -- and the close is completed. A card already landed counts as already;
  -- a refused move is a finding line and the card stays where it was.
  local function gh_issue_closed(keys, args)
    local key, stream = keys[1] or '', keys[2] or ''
    local group, entry, repo, n = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
    local sender, reason, at, landers = args[5] or '', args[6] or '', args[7] or '', args[8] or ''
    if not string.match(key, '^issue:[^:]+:[0-9]+:cards$') or stream == '' or group == '' or entry == '' then
      return redis.error_reply('ERR ns_gh_issue_closed: want issue:<name>:<n>:cards, a stream, a group and an entry id')
    end
    local cards = redis.call('ZRANGE', key, 0, -1)
    local ours = false
    for l in string.gmatch(landers, '%S+') do
      if l == sender then ours = true end
    end
    local out
    if not ours or sender == '' then
      gh_finding(entry, repo, n, sender, 'not-lander', 'closed by ' .. sender .. ', not the lander', #cards, at)
      out = { 'FINDING', 'not-lander', tostring(#cards) }
    elseif reason ~= '' and reason ~= 'completed' then
      gh_finding(entry, repo, n, sender, 'not-completed', 'state_reason ' .. reason, #cards, at)
      out = { 'FINDING', 'not-completed', tostring(#cards) }
    else
      local landed, already, refused = 0, 0, 0
      for _, id in ipairs(cards) do
        local c = redis.call('HMGET', id, 'state', 'where', 'where_ok')
        if c[1] == 'landed' then
          already = already + 1
        elseif c[1] and not (c[2] == 'done' and c[3] == 'fail') then
          local err = CARD.move(id, 'done', { state = 'landed', ok = 'ok', by = 'gh-ingest',
            why = 'issue ' .. repo .. '#' .. n .. ' closed by ' .. sender })
          if err then
            refused = refused + 1
            gh_finding(entry, repo, n, sender, 'refused', id .. ' ' .. err, #cards, at)
          else
            landed = landed + 1
          end
        end
      end
      if landed + already + refused == 0 then
        gh_finding(entry, repo, n, sender, 'no-card', 'no live card for the issue', #cards, at)
        out = { 'FINDING', 'no-card', tostring(#cards) }
      else
        if landed > 0 then redis.call('HINCRBY', 'gh:ingest', 'landed', landed) end
        gh_count(landed > 0 and 'applied' or 'kept', entry)
        out = { 'LANDED', tostring(landed), tostring(already), tostring(refused) }
      end
    end
    redis.call('XACK', stream, group, entry)
    return out
  end

  -- ns_gh_issue_index(S): backfill issue:<name>:<n>:cards for every card of
  -- the sprint (one walk of sprint:<S>:cards, the roster; never a SCAN), for
  -- the cards created before card_create wrote the index. ZADD NX: an id
  -- already indexed keeps its score. -> number of ids added.
  local function gh_issue_index(keys, args)
    local S = args[1] or ''
    if not string.match(S, '^[-a-z0-9]+$') then
      return redis.error_reply('ERR ns_gh_issue_index: want a sprint name')
    end
    local added = 0
    for _, id in ipairs(redis.call('ZRANGE', 'sprint:' .. S .. ':cards', 0, -1)) do
      local c = redis.call('HMGET', id, 'origin', 'created_at')
      local ik = CARD.issue_key(c[1])
      if ik then
        added = added + redis.call('ZADD', ik, 'NX', tonumber(c[2]) or 0, id)
      end
    end
    return added
  end

  redis.register_function('ns_gh_pr', gh_pr)
  redis.register_function('ns_gh_issue_closed', gh_issue_closed)
  redis.register_function('ns_gh_issue_index', gh_issue_index)
end
