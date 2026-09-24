-- `card harvest --orphans` for #3042 (spec #2756 3.2 row "orphan-effect",
-- 4.3, controls 25 and 26): the bench harvest worker's sweep over its
-- orphan-effect cards. No shebang: loader.go prepends the one library header;
-- one do-block so its locals never add to the shared chunk's local count.
--
-- Only an end record of the card's own identity ends an orphan (ns_card_end
-- record mode, card_run.lua). These functions list what the sweep must look
-- at and settle the unresolved item <label>:orphan-effect:<attempt> once the
-- orphan is over: ENDED by its own record, or SUPERSEDED when a later attempt
-- of the same label ended DONE (its branch renamed orphan/<S>/<label>-a<n>
-- and its PR closed first, by the caller, under the same lease).
do
  local function ho_hget(key, field)
    local v = redis.call('HGET', key, field)
    if not v then return '' end
    return tostring(v)
  end

  local function ho_now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function ho_lease_ok(bench, instance, token)
    local key = 'lease:harvest:' .. bench
    return instance ~= '' and token ~= ''
      and ho_hget(key, 'instance') == instance and ho_hget(key, 'token') == token
  end

  -- identity <S>/<label>/<base8>/<bench>/<attempt> -> bench, attempt
  local function ho_bench_attempt(identity)
    local bench, a = string.match(identity, '^[^/]+/[^/]+/[^/]+/([^/]+)/([0-9]+)$')
    return bench, a
  end

  -- The unresolved item's value is "<identity> <evidence>".
  local function ho_item_identity(value)
    return string.match(value or '', '^(%S+)') or ''
  end

  -- ns_orphan_due S bench (read only)
  -- Rows of 6: kind label attempt identity repo age_ms.
  --   ORPHAN     the card is orphan-effect on this bench: re-read its record
  --   ENDED      its own record already ended it; the item is still open
  --   SUPERSEDE  a later attempt of the label ended DONE: rename and close
  -- age_ms is Redis TIME minus orphan_at (ORPHAN rows only), so the caller
  -- never uses its own clock for orphan_grace.
  redis.register_function{
    function_name = 'ns_orphan_due',
    flags = { 'no-writes' },
    callback = function(keys, args)
      local S, bench = args[1] or '', args[2] or ''
      if S == '' or bench == '' then return { 'USAGE' } end
      local now = ho_now_ms()
      local out = { 'OK' }
      local seen = {}
      local labels = redis.call('SMEMBERS', 's:' .. S .. ':idx:card:orphan-effect')
      table.sort(labels)
      for _, label in ipairs(labels) do
        local f = redis.call('HMGET', 's:' .. S .. ':card:' .. label,
          'state', 'bench', 'attempt', 'identity', 'repo', 'orphan_at')
        if (f[1] or '') == 'orphan-effect' and (f[2] or '') == bench then
          local age = now - (tonumber(f[6] or '') or now)
          local row = { 'ORPHAN', label, f[3] or '', f[4] or '', f[5] or '', tostring(age) }
          for _, v in ipairs(row) do out[#out + 1] = v end
          seen[label .. ':orphan-effect:' .. (f[3] or '')] = true
        end
      end
      local items = redis.call('HGETALL', 's:' .. S .. ':unresolved')
      local rows = {}
      for i = 1, #items, 2 do
        local field, value = items[i], items[i + 1]
        local label, a = string.match(field, '^(.+):orphan%-effect:([0-9]+)$')
        if label and not seen[field] then
          local identity = ho_item_identity(value)
          local ibench, ia = ho_bench_attempt(identity)
          if ibench == bench and ia == a then
            local f = redis.call('HMGET', 's:' .. S .. ':card:' .. label, 'state', 'attempt', 'outcome', 'repo')
            local state, cur, outcome = f[1] or '', tonumber(f[2] or '') or 0, f[3] or ''
            local done = state == 'ended' or state == 'harvested'
            local kind = ''
            if done and cur == tonumber(a) then
              kind = 'ENDED'
            elseif done and cur > tonumber(a) and outcome == 'DONE' then
              kind = 'SUPERSEDE'
            end
            if kind ~= '' then
              rows[#rows + 1] = { kind, label, a, identity, f[4] or '', '0' }
            end
          end
        end
      end
      table.sort(rows, function(x, y)
        if x[2] ~= y[2] then return x[2] < y[2] end
        return tonumber(x[3]) < tonumber(y[3])
      end)
      for _, r in ipairs(rows) do
        for _, v in ipairs(r) do out[#out + 1] = v end
      end
      return out
    end,
  }

  -- ns_orphan_settle S bench instance token label attempt mode evidence actor
  -- mode ended: the card's own record ended attempt <attempt> (state ended or
  --   harvested at that attempt) -> the item is closed with one receipt.
  -- mode superseded: a later attempt ended DONE and the caller has renamed the
  --   branch and closed its PR -> the item is closed, s:<S>:superseded records
  --   identity -> evidence, one receipt.
  -- Idempotent under orphan:<identity>: a repeat returns the stored receipt.
  -- Replies <status>|<receipt>: OK, GONE (no item: settled before), FENCED,
  -- NOTFOUND, STATE, CONFLICT, USAGE.
  redis.register_function('ns_orphan_settle', function(keys, args)
    local S, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
    local label, a, mode = args[5] or '', args[6] or '', args[7] or ''
    local evidence, actor = args[8] or '', args[9] or 'card-harvest-orphans'
    if S == '' or label == '' or not string.match(a, '^[1-9][0-9]*$') or (mode ~= 'ended' and mode ~= 'superseded') then
      return 'USAGE|'
    end
    if not ho_lease_ok(bench, instance, token) then return 'FENCED|' end
    local unresolved = 's:' .. S .. ':unresolved'
    local item = label .. ':orphan-effect:' .. a
    local value = ho_hget(unresolved, item)
    -- No item: an earlier pass under this lease settled it.
    if value == '' then return 'GONE|' end
    local identity = ho_item_identity(value)
    local ibench, ia = ho_bench_attempt(identity)
    if ibench ~= bench or ia ~= a then return 'CONFLICT|' end
    local idem_key = 's:' .. S .. ':idem'
    local idem = 'orphan:' .. identity
    local prev = ho_hget(idem_key, idem)
    if prev ~= '' then
      redis.call('HDEL', unresolved, item)
      return 'OK|' .. prev
    end
    local key = 's:' .. S .. ':card:' .. label
    local state = ho_hget(key, 'state')
    if state == '' then return 'NOTFOUND|' end
    local cur = tonumber(ho_hget(key, 'attempt')) or 0
    local done = state == 'ended' or state == 'harvested'
    local to
    if mode == 'ended' then
      if not done or cur ~= tonumber(a) then return 'STATE|' end
      to = 'ended'
      evidence = 'outcome=' .. ho_hget(key, 'outcome') .. ' reason=' .. ho_hget(key, 'reason')
    else
      if not done or cur <= tonumber(a) or ho_hget(key, 'outcome') ~= 'DONE' then return 'STATE|' end
      if evidence == '' then return 'USAGE|' end
      to = 'superseded'
    end
    local at = ho_now_ms()
    local receipt = redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'orphan', 'id', label, 'from', 'orphan-effect', 'to', to,
      'attempt', a, 'token_sha', '', 'actor', actor, 'reason', to,
      'evidence', identity .. ' ' .. evidence, 'idem', idem, 'at', tostring(at))
    redis.call('HDEL', unresolved, item)
    if mode == 'superseded' then
      redis.call('HSET', 's:' .. S .. ':superseded', identity, evidence)
    end
    redis.call('HSET', idem_key, idem, receipt)
    return 'OK|' .. receipt
  end)
end
