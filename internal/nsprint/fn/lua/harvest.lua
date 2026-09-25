-- Card harvest for #2932 (spec #2756 3.2 row "ended(DONE) -> harvested",
-- 4.3 `card harvest`, 5.4 rows "branch push", "PR open" and "harvest worker").
-- No shebang: loader.go prepends the one library header. The file is one
-- do-block so its locals never add to the shared chunk's local count.
--
-- One harvest worker per bench holds lease:harvest:<b>; every write below
-- checks that lease, so two workers for one bench can never both harvest.
-- The PR idempotency key is pr:<repo>:<branch> in s:<S>:idem; a re-run reads
-- it back and never opens a second PR. The harvested transition requires the
-- PR head to equal the card's pushed_sha.
--
-- The PR record pr:<name>:<n> (the bare repository name, internal/nsprint/
-- prkey; a hash: head, base, base_sha, stream, label, sprint, branch, state,
-- at) is written once by ns_harvest_pr, in the same
-- call as the reservation, from the head a REST response verified. It is what
-- the lander and a later harvest pass read: after it exists neither needs
-- GitHub for the head. One writer, written once, no TTL. The card model
-- (rowan-new specs/ws-index.md, "The card model") puts pr and head on the
-- card record too: the same call writes the card's pr and head fields, so
-- card -> PR (pr, head) and PR -> card (label, sprint) are one double link,
-- written together or not at all; ns_card_harvested writes the same pr and
-- head again with the receipt.
do
  -- Every card state write here is NS.card (02_card_move.lua): harvested is
  -- done -> done (the outcome kept), a refusal is any -> done/fail.
  local CARD = NS.card
  -- A PR opening is an event for the sprint's tasks too (03_task_event.lua).
  local TEV = NS.tev

  local function hv_now_ms()
    local t = redis.call('TIME')
    return string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
  end

  local function hv_hget(key, field)
    local v = redis.call('HGET', key, field)
    if not v then return '' end
    return tostring(v)
  end

  -- hv_prkey is the one PR record key, pr:<name>:<n> with the bare
  -- repository name (internal/nsprint/prkey, Key; land_stream.lua and
  -- route_duty.lua strip the owner the same way). A card's repo is
  -- owner/name (mas-bandwidth/nova-tools); the record never is (#3740). The
  -- idem field pr:<repo>:<branch> in s:<S>:idem is another record and keeps
  -- the card's repo as it is.
  local function hv_prkey(repo, n)
    return 'pr:' .. (string.match(repo, '([^/]+)$') or repo) .. ':' .. n
  end

  local function hv_branch(S, label, attempt)
    return 'nova/' .. S .. '/' .. label .. '-a' .. attempt
  end

  -- cr_split: 'a,b,c' -> {'a','b','c'}; empty items dropped.
  local function cr_split(s)
    local out = {}
    for item in string.gmatch(s or '', '[^,]+') do
      if item ~= '' then out[#out + 1] = item end
    end
    return out
  end

  -- cr_hget: HGET that returns '' on nil.
  local function cr_hget(key, field)
    local v = redis.call('HGET', key, field)
    if not v then return '' end
    return tostring(v)
  end

  local function hv_lease_ok(bench, instance, token)
    local key = 'lease:harvest:' .. bench
    return instance ~= '' and token ~= ''
      and hv_hget(key, 'instance') == instance and hv_hget(key, 'token') == token
  end

  -- The durable harvest steps (#2932 rev 2): a card on its way to harvested
  -- moves forward only, absent -> pushed -> intent -> published -> harvested.
  -- pushed and intent are ns_harvest_step's; published is ns_harvest_pr's
  -- (with the verified reservation, same call); harvested is
  -- ns_card_harvested's (with the receipt).
  local hv_rank = { [''] = 0, pushed = 1, intent = 2, published = 3, harvested = 4 }

  -- ns_harvest_lease bench instance token ttl_ms
  -- TAKEN on a free lease, RENEWED for the holder, HELD|<instance> otherwise.
  -- The holder's name is kept in proc:harvest:<b> holder from the take until
  -- its pass line or release clears it (#3737), so a lease that lapsed on its
  -- TTL with its holder still named was never given back: the take answers
  -- TAKEN|<that instance> and the worker logs `TAKEN from=<instance> stale`.
  redis.register_function('ns_harvest_lease', function(keys, args)
    local bench, instance, token, ttl = args[1] or '', args[2] or '', args[3] or '', tonumber(args[4] or '6000')
    if bench == '' or instance == '' or token == '' or not ttl or ttl <= 0 then
      return 'USAGE'
    end
    local key = 'lease:harvest:' .. bench
    local at = hv_now_ms()
    if redis.call('EXISTS', key) == 1 then
      if not hv_lease_ok(bench, instance, token) then
        return 'HELD|' .. hv_hget(key, 'instance')
      end
      redis.call('HSET', key, 'at', at)
      redis.call('PEXPIRE', key, ttl)
      return 'RENEWED'
    end
    local proc = 'proc:harvest:' .. bench
    local stale = hv_hget(proc, 'holder')
    redis.call('HSET', key, 'instance', instance, 'token', token, 'at', at)
    redis.call('PEXPIRE', key, ttl)
    redis.call('HSET', proc, 'holder', instance, 'holder_at', at)
    if stale ~= '' then
      redis.call('HSET', proc, 'stale_from', stale, 'stale_at', at)
      return 'TAKEN|' .. stale
    end
    return 'TAKEN'
  end)

  -- ns_harvest_pass bench instance token took_ms n err [left]
  -- The worker's pass line (proc:harvest:<b>, written by that worker only)
  -- and the lease release, in one call. left (#3737) is the due cards the
  -- pass never started (the reconciler lease bound); absent reads 0. A
  -- fenced worker writes nothing.
  redis.register_function('ns_harvest_pass', function(keys, args)
    local bench, instance, token = args[1] or '', args[2] or '', args[3] or ''
    local took, n, err = args[4] or '0', args[5] or '0', args[6] or ''
    local left = args[7] or '0'
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED'
    end
    local at = hv_now_ms()
    redis.call('HSET', 'proc:harvest:' .. bench, 'pass_at', at, 'took_ms', took, 'n', n, 'err', err,
      'left', left, 'holder', '', 'at', at)
    redis.call('DEL', 'lease:harvest:' .. bench)
    return 'OK'
  end)

  -- ns_harvest_release bench instance token (#3737)
  -- The holder gives lease:harvest:<b> back without a pass line: a reconciler
  -- exiting FENCED releases what a worker it could not wait for still holds.
  -- RELEASED, or FENCED (another holder, or none) and nothing is touched.
  redis.register_function('ns_harvest_release', function(keys, args)
    local bench, instance, token = args[1] or '', args[2] or '', args[3] or ''
    if bench == '' then
      return 'USAGE'
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED'
    end
    redis.call('DEL', 'lease:harvest:' .. bench)
    redis.call('HSET', 'proc:harvest:' .. bench, 'holder', '', 'released_at', hv_now_ms())
    return 'RELEASED'
  end)

  -- ns_harvest_due S bench limit (read only)
  -- The bench's ended(DONE) cards with a commit, oldest label order, as rows
  -- of 14: label repo base attempt pushed_sha identity results branch pr_idem
  -- harvest_step base_sha stream done_when rec_head. stream and done_when are
  -- the card's STREAM: and DONE-WHEN: lines (ns_card_header), for the PR
  -- body; rec_head is the head of the PR record pr:<name>:<pr_idem>, or ''
  -- when the idem key names no PR or the record is not written yet.
  -- A ci card (kind script) and a card with no pushed_sha have nothing to
  -- harvest; ok-to-friend classifies them (3.2). The bench's host and user
  -- from its beat come first so the worker needs no second read.
  redis.register_function{
    function_name = 'ns_harvest_due',
    flags = { 'no-writes' },
    callback = function(keys, args)
      local S, bench, limit = args[1] or '', args[2] or '', tonumber(args[3] or '256')
      if S == '' or bench == '' or not limit or limit <= 0 then
        return { 'USAGE' }
      end
      local bstate = redis.call('HGET', 'bench:' .. bench .. ':state', 'state')
      if not bstate or bstate == '' then
        bstate = 'down'
      else
        bstate = string.lower(bstate)
      end
      if bstate ~= 'up' then
        return { 'NONE', bstate }
      end
      local beat = 'bench:' .. bench .. ':beat'
      local out = { 'OK', hv_hget(beat, 'host'), hv_hget(beat, 'user') }
      local labels = redis.call('SINTER', 's:' .. S .. ':bench:' .. bench .. ':ended', 's:' .. S .. ':idx:card:ended')
      table.sort(labels)
      local rows = 0
      for _, label in ipairs(labels) do
        local key = 's:' .. S .. ':card:' .. label
        local f = redis.call('HMGET', key, 'state', 'outcome', 'kind', 'bench', 'repo', 'base',
          'attempt', 'pushed_sha', 'identity', 'results', 'harvest_step', 'base_sha', 'stream', 'done_when')
        local state, outcome, kind, cbench = f[1] or '', f[2] or '', f[3] or '', f[4] or ''
        local pushed = f[8] or ''
        local repo, attempt = f[5] or '', f[7] or ''
        local res_key = 's:' .. S .. ':card:' .. label .. ':result:a' .. attempt
        local valid = hv_hget(res_key, 'valid')
        -- pushed_sha '-' is a card that committed nothing (a read card, a
        -- probe, NO-COMMIT or OVERSIZE): it never belongs to harvest; its
        -- friend read is #3036's report rule (#2932 rev 4).
        if (valid == '1' or valid == '') and state == 'ended' and outcome == 'DONE' and kind ~= 'script'
          and cbench == bench and pushed ~= '' and pushed ~= '-' then
          if rows >= limit then break end
          local branch = hv_branch(S, label, attempt)
          out[#out + 1] = label
          out[#out + 1] = repo
          out[#out + 1] = f[6] or ''
          out[#out + 1] = attempt
          out[#out + 1] = pushed
          out[#out + 1] = f[9] or ''
          out[#out + 1] = f[10] or ''
          out[#out + 1] = branch
          local idem_pr = hv_hget('s:' .. S .. ':idem', 'pr:' .. repo .. ':' .. branch)
          out[#out + 1] = idem_pr
          out[#out + 1] = f[11] or ''
          out[#out + 1] = f[12] or ''
          out[#out + 1] = f[13] or ''
          out[#out + 1] = f[14] or ''
          if idem_pr ~= '' then
            out[#out + 1] = hv_hget(hv_prkey(repo, idem_pr), 'head')
          else
            out[#out + 1] = ''
          end
          rows = rows + 1
        end
      end
      return out
    end,
  }

  -- ns_harvest_step S bench instance token label step
  -- The lease holder's durable step before the PR exists: pushed (once
  -- ls-remote on the bench shows the branch tip == pushed_sha) and intent
  -- (right before the REST create POST). Forward only: the same step again is
  -- a no-op (OK, nothing written), a step behind the card's is BACKWARD, and
  -- published and harvested are REFUSED here (they have their own writers).
  redis.register_function('ns_harvest_step', function(keys, args)
    local S, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
    local label, step = args[5] or '', args[6] or ''
    if S == '' or bench == '' or label == '' or step == '' then
      return 'USAGE|'
    end
    if step ~= 'pushed' and step ~= 'intent' then
      return 'REFUSED|' .. step
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED|'
    end
    local key = 's:' .. S .. ':card:' .. label
    local f = redis.call('HMGET', key, 'state', 'outcome', 'bench', 'harvest_step')
    local state, outcome, cbench, cur = f[1] or '', f[2] or '', f[3] or '', f[4] or ''
    if state == '' then return 'NOTFOUND|' end
    if state ~= 'ended' or outcome ~= 'DONE' then return 'STATE|' .. state end
    if cbench ~= bench then return 'CONFLICT|' .. cbench end
    local have, want = hv_rank[cur] or 0, hv_rank[step]
    if want < have then return 'BACKWARD|' .. cur end
    if want == have then return 'OK|' .. cur end
    local at = hv_now_ms()
    if step == 'intent' then
      redis.call('HSET', key, 'harvest_step', step, 'harvest_step_at', at, 'harvest_intent_at', at)
    else
      redis.call('HSET', key, 'harvest_step', step, 'harvest_step_at', at)
    end
    return 'OK|' .. step
  end)

  -- ns_harvest_pr S bench instance token label repo branch pr [head]
  -- The reservation comes after the PR (#2932 rule 1): the caller passes only
  -- a number GitHub returned whose head.ref and head.sha a REST response
  -- verified. HSETNX pr:<repo>:<branch>; when the key then names this PR the
  -- card moves to published in the same call with its pr and head fields,
  -- and the PR record pr:<name>:<pr> is written once (head, base, base_sha,
  -- stream, label, sprint, branch, state=open, at) from that verified head,
  -- which must be the card's pushed_sha (HEAD otherwise, nothing written; an
  -- absent head argument is the pushed_sha itself). A key that already names another PR
  -- is returned unchanged and nothing is written (the caller reports it and
  -- opens nothing). A card with no harvest_step (never pushed) or one past
  -- published is refused (STEP), and a fenced caller writes nothing. The
  -- same call moves every sprint task naming the PR or the card's origin
  -- issue to merging (NS.tev, nova-tools#3779).
  redis.register_function('ns_harvest_pr', function(keys, args)
    local S, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
    local label, repo, branch, pr = args[5] or '', args[6] or '', args[7] or '', args[8] or ''
    local head = args[9] or ''
    if S == '' or label == '' or repo == '' or branch == '' or not string.match(pr, '^[1-9][0-9]*$') then
      return 'USAGE|'
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED|'
    end
    local key = 's:' .. S .. ':card:' .. label
    local f = redis.call('HMGET', key, 'state', 'bench', 'attempt', 'repo', 'harvest_step',
      'pushed_sha', 'base', 'base_sha', 'stream', 'origin')
    local state, cbench, attempt, crepo, cur = f[1] or '', f[2] or '', f[3] or '', f[4] or '', f[5] or ''
    local pushed = f[6] or ''
    -- No card, no landing (#3915): a PR whose branch no card names is not
    -- work; NOCOPY writes nothing.
    if state == '' then return 'NOCOPY|no card ' .. key end
    if cbench ~= bench or crepo ~= repo then
      return 'CONFLICT|'
    end
    if hv_branch(S, label, attempt) ~= branch then
      return 'NOCOPY|' .. branch .. ' is not ' .. key .. "'s branch " .. hv_branch(S, label, attempt)
    end
    if cur ~= 'pushed' and cur ~= 'intent' and cur ~= 'published' then
      return 'STEP|' .. cur
    end
    if head == '' then head = pushed end
    if head ~= pushed or pushed == '' or pushed == '-' then return 'HEAD|' .. pushed end
    local idem = 's:' .. S .. ':idem'
    local ikey = 'pr:' .. repo .. ':' .. branch
    redis.call('HSETNX', idem, ikey, pr)
    local stored = hv_hget(idem, ikey)
    if stored == pr then
      local at = hv_now_ms()
      if cur ~= 'published' then
        redis.call('HSET', key, 'harvest_step', 'published', 'harvest_step_at', at, 'pr', pr, 'head', head)
      end
      -- The card's origin issue: the PR body closes it when it is in the
      -- PR's repository (harvest/body.go originLine), so the record carries
      -- it as closes for the lander; either way a task naming it names this
      -- work.
      local orepo, onum = TEV.TR.parse(f[10] or '', '')
      local closes = '-'
      if orepo and orepo == TEV.TR.bare(repo) then closes = onum end
      local rkey = hv_prkey(repo, pr)
      if redis.call('EXISTS', rkey) == 0 then
        redis.call('HSET', rkey, 'head', head, 'base', f[7] or '', 'base_sha', f[8] or '',
          'stream', f[9] or '', 'label', label, 'sprint', S, 'branch', branch, 'state', 'open', 'at', at,
          'closes', closes)
      end
      -- Every sprint task naming this PR or the card's origin moves to
      -- merging in this same fenced call (a repeat finds them there: SAME).
      local refs = TEV.refs(repo, pr, '')
      if orepo then refs[#refs + 1] = orepo .. '#' .. tonumber(onum) end
      TEV.opened(refs, 'harvest', 'PR opened: ' .. TEV.TR.bare(repo) .. '#' .. pr .. ' (' .. label .. ')')
    end
    return 'PR|' .. stored
  end)

  -- ns_card_harvested S label bench instance token pr head actor [pr_body]
  -- ended(DONE) -> harvested (3.2): the bench's lease holder only, the card
  -- ended DONE on this bench, the PR recorded under the card's idem key, and
  -- the head read back by REST equal to pushed_sha. A repeat with the same PR
  -- and head returns the stored receipt and writes nothing. pr_body (#3712),
  -- when given, is the body this harvest opened the PR with, stored on the
  -- record with pr and head; the same call writes the `pr head` log entry in
  -- ns_pr_head's shape (kind repo pr head prev source at).
  redis.register_function('ns_card_harvested', function(keys, args)
    local S, label, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or '', args[5] or ''
    local pr, head, actor = args[6] or '', args[7] or '', args[8] or 'card-harvest'
    local pr_body = args[9] or ''
    if S == '' or label == '' or bench == '' or pr == '' or head == '' then
      return 'USAGE|'
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED|'
    end
    local key = 's:' .. S .. ':card:' .. label
    local state = hv_hget(key, 'state')
    if state == '' then return 'NOTFOUND|' end
    local attempt = hv_hget(key, 'attempt')
    local identity = hv_hget(key, 'identity')
    local idem_key = 's:' .. S .. ':idem'
    local idem = 'harvest:' .. identity
    if state == 'harvested' then
      if hv_hget(key, 'pr') == pr and hv_hget(key, 'head') == head then
        return 'OK|' .. hv_hget(idem_key, idem)
      end
      return 'CONFLICT|'
    end
    if state ~= 'ended' or hv_hget(key, 'outcome') ~= 'DONE' then return 'STATE|' end
    if hv_hget(key, 'bench') ~= bench then return 'CONFLICT|' end
    if head ~= hv_hget(key, 'pushed_sha') then return 'HEAD|' end
    local repo = hv_hget(key, 'repo')
    local branch = hv_branch(S, label, attempt)
    if hv_hget(idem_key, 'pr:' .. repo .. ':' .. branch) ~= pr then return 'IDEM|' end

    local at = hv_now_ms()
    local fields = { 'pr', pr, 'head', head, 'harvested_at', at,
      'harvest_step', 'harvested', 'harvest_step_at', at }
    if pr_body ~= '' then
      fields[#fields + 1] = 'pr_body'
      fields[#fields + 1] = pr_body
    end
    if CARD.move(key, 'done', { state = 'harvested', by = actor, why = 'harvested', fields = fields }) then
      return 'STATE|'
    end
    local receipt = redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'card', 'id', label, 'from', 'ended', 'to', 'harvested',
      'attempt', attempt, 'token_sha', hv_hget(key, 'token_sha'),
      'actor', actor, 'reason', 'harvested',
      'evidence', 'pr=' .. pr .. ' head=' .. head .. ' branch=' .. branch,
      'idem', idem, 'at', at)
    redis.call('HSET', key, 'harvest_receipt', receipt)
    redis.call('HSET', 's:' .. S .. ':prcard', repo .. '#' .. pr, label) -- pr-to-read skips card PRs (#3040)
    redis.call('HSET', idem_key, idem, receipt)
    -- The PR's first head, in the shape ns_pr_head writes and pr-to-read's
    -- readHeadEvents parses; prev is empty: harvest opened the PR at head.
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'pr head', 'repo', repo, 'pr', pr, 'head', head,
      'prev', '', 'source', 'harvest', 'at', at)
    -- The CI request for this head, idempotent on the sha (nova-tools#3717):
    -- if ci:<repo>:<head> already exists, nothing is written; otherwise the
    -- record is created with ci=pending and the member is added to ci:pool.
    local ci_key = 'ci:' .. repo .. ':' .. head
    if redis.call('EXISTS', ci_key) == 0 then
      local declared = redis.call('HGET', 'cfg:ci:' .. repo, 'checks')
      if declared == '' then
        -- defaults from internal/nsprint/ci/run.go Defaults
        if repo == 'nova-tools' then
          redis.call('HSET', 'cfg:ci:' .. repo, 'checks', 'go-build,go-vet,go-test-cmd,go-test-internal,internal-ci',
            'cmd:go-build', 'go build ./...', 'cmd:go-vet', 'go vet ./...',
            'cmd:go-test-cmd', 'go test -count=1 ./cmd/...', 'cmd:go-test-internal', 'go test -count=1 ./internal/...',
            'cmd:internal-ci', 'go test -count=1 ./internal/ci/...')
        elseif repo == 'rowan-tools' then
          redis.call('HSET', 'cfg:ci:' .. repo, 'checks', 'shellcheck,bats',
            'cmd:shellcheck', 'shellcheck -S warning bin', 'cmd:bats', 'bats tests')
        end
      end
      redis.call('HSET', ci_key, 'repo', repo, 'sha', head, 'pr', pr, 'url', '',
        'checks', redis.call('HGET', 'cfg:ci:' .. repo, 'checks') or 'go-build,go-vet,go-test-cmd,go-test-internal,internal-ci',
        'requested_at', tostring(at), 'ci', 'pending',
        'attempt', '0', 'bench', '', 'token', '', 'lease_until', '0', 'claimed_at', '0', 'ended_at', '0')
      local checks = redis.call('HGET', 'cfg:ci:' .. repo, 'checks')
      if checks == '' then checks = 'go-build,go-vet,go-test-cmd,go-test-internal,internal-ci' end
      for _, name in ipairs(cr_split(checks)) do
        local cmd = redis.call('HGET', 'cfg:ci:' .. repo, 'cmd:' .. name)
        if cmd ~= '' then redis.call('HSET', ci_key, 'cmd:' .. name, cmd) end
      end
      redis.call('ZADD', 'ci:pool', at, repo .. ':' .. head)
    end
    return 'OK|' .. receipt
  end)

  -- ns_harvest_fail S label bench instance token code err cap
  -- One failed harvest pass for an ended(DONE) card (#3712): the lease holder
  -- counts it in harvest_fails and records the err line. Below cap it answers
  -- COUNT|<n> and the next pass retries; at cap the card moves to done/fail
  -- through NS.card (state refused, reason harvest, the err line as why) and
  -- leaves the bench's ended set, FAILED|<n>, so no card loops silently.
  redis.register_function('ns_harvest_fail', function(keys, args)
    local S, label, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or '', args[5] or ''
    local code, err, cap = args[6] or '', args[7] or '', tonumber(args[8] or '3')
    if S == '' or label == '' or bench == '' or not cap or cap < 1 then
      return 'USAGE|'
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED|'
    end
    local key = 's:' .. S .. ':card:' .. label
    local f = redis.call('HMGET', key, 'state', 'bench')
    local state, cbench = f[1] or '', f[2] or ''
    if state == '' then return 'NOTFOUND|' end
    if state ~= 'ended' then return 'STATE|' .. state end
    if cbench ~= bench then return 'CONFLICT|' .. cbench end
    local line = code .. ': ' .. err
    local at = hv_now_ms()
    local n = redis.call('HINCRBY', key, 'harvest_fails', 1)
    redis.call('HSET', key, 'harvest_err', line, 'harvest_err_at', at)
    if n < cap then return 'COUNT|' .. n end
    local moved = CARD.move(key, 'done', { state = 'refused', ok = 'fail', by = 'card-harvest', why = line,
      fields = { 'reason', 'harvest', 'refused_field', 'harvest', 'refused_defect', code, 'refused_at', at } })
    if moved then return 'MOVE|' .. moved end
    local receipt = redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'card', 'id', label, 'from', 'ended', 'to', 'refused',
      'attempt', hv_hget(key, 'attempt'), 'token_sha', hv_hget(key, 'token_sha'),
      'actor', 'card-harvest', 'reason', 'harvest', 'evidence', line,
      'idem', 'harvest-fail:' .. S .. ':' .. label .. ':' .. hv_hget(key, 'attempt'), 'at', at)
    redis.call('HSET', key, 'refused_receipt', receipt)
    redis.call('SREM', 's:' .. S .. ':bench:' .. bench .. ':ended', label)
    redis.call('SADD', 's:' .. S .. ':bench:' .. bench .. ':refused', label)
    return 'FAILED|' .. n
  end)

  -- ns_harvest_refuse S label bench instance token field defect
  -- Marks a card whose result validation failed as refused:
  -- state=refused, refused_field=field, refused_defect=defect, refused_at=at
  -- Moves from ended to refused index, logs transition.
  redis.register_function('ns_harvest_refuse', function(keys, args)
    local S, label, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or '', args[5] or ''
    local field, defect = args[6] or '', args[7] or ''
    if S == '' or label == '' or bench == '' then
      return 'USAGE'
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED'
    end
    local key = 's:' .. S .. ':card:' .. label
    local state = hv_hget(key, 'state')
    if state == '' then return 'NOTFOUND' end
    if state == 'refused' then return 'OK' end

    local at = hv_now_ms()
    if CARD.move(key, 'done', { state = 'refused', ok = 'fail', by = 'card-harvest', why = 'refused',
        fields = { 'refused_field', field, 'refused_defect', defect, 'refused_at', at } }) then
      return 'STATE'
    end
    local attempt = hv_hget(key, 'attempt')
    local token_sha = hv_hget(key, 'token_sha')
    local log_key = 's:' .. S .. ':log'
    local receipt = redis.call('XADD', log_key, '*',
      'kind', 'card', 'id', label, 'from', state, 'to', 'refused',
      'attempt', attempt, 'token_sha', token_sha, 'actor', 'card-harvest',
      'reason', 'refused', 'evidence', 'field=' .. field .. ' defect=' .. defect,
      'idem', 'refuse:' .. S .. ':' .. label .. ':' .. attempt, 'at', at)

    redis.call('HSET', key, 'refused_receipt', receipt)

    redis.call('SREM', 's:' .. S .. ':bench:' .. bench .. ':' .. state, label)
    redis.call('SADD', 's:' .. S .. ':bench:' .. bench .. ':refused', label)
    return 'OK'
  end)
end
