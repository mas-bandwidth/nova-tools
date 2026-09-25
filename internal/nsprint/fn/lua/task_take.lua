-- Batched task take (#3261). `task take` and the 1 s presence loop used to
-- spend 4 + S + 2k round trips: the friend reads, sprint:order, one rank read
-- pair per sprint, then an attempt read and one ns_task_take per claim. Now
-- Go sends one pipeline (friend reads plus ns_task_take_view below), ranks in
-- memory, and claims the whole ranked list in one ns_task_take_n call.

-- CL is task_claim.lua's table: CL.take is the guarded one-task take.
local CL = NS.claim

-- rank_fields are the task hash fields deal.RankSnapshot reads, in its
-- order, then attempt, which the Go side needs for each fence token.
local rank_fields = { 'title', 'est', 'priority', 'pushed_at', 'owner', 'state', 'attempt' }

-- take_view: args friend, sprint, id. A friend with no free slot gets an
-- empty reply. The sprints are the one named or
-- sprint:order; a sprint whose status is not open is skipped (ns_task_take
-- would answer NONE for every task in it). Per sprint the reply is
-- { S, open, fields }: open is the friend's open queue as a flat
-- member, score list, fields a flat id, title, est, priority, pushed_at,
-- owner, state, attempt list over the queue and the open, claimed and
-- working indexes (the dependency edges). With id set, open and the index
-- walk are skipped and fields holds that id alone. Read-only and bounded by
-- the sprints' live tasks: no SCAN, no KEYS.
local function take_view(keys, args)
  local friend, sprint, id = args[1], args[2] or '', args[3] or ''
  -- A friend with no free slot takes nothing: the view reads no queue.
  local desired = tonumber(redis.call('HGET', 'friend:' .. friend .. ':desired', 'slots') or '0') or 0
  if desired - redis.call('ZCARD', 'friend:' .. friend .. ':starting') -
      redis.call('ZCARD', 'friend:' .. friend .. ':living') <= 0 then
    return {}
  end
  local sprints = { sprint }
  if sprint == '' then
    sprints = redis.call('ZRANGE', 'sprint:order', 0, -1)
  end
  local out = {}
  for _, S in ipairs(sprints) do
    if S ~= '' and redis.call('HGET', 's:' .. S, 'status') == 'open' then
      local open, ids = {}, {}
      if id ~= '' then
        ids[1] = id
      else
        open = redis.call('ZRANGE', 's:' .. S .. ':open:' .. friend, 0, -1, 'WITHSCORES')
        if #open > 0 then
          local seen = {}
          for i = 1, #open, 2 do
            seen[open[i]] = true
          end
          for _, state in ipairs({ 'open', 'claimed', 'working' }) do
            for _, member in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':idx:task:' .. state)) do
              seen[member] = true
            end
          end
          for member in pairs(seen) do
            ids[#ids + 1] = member
          end
        end
      end
      local fields = {}
      for _, tid in ipairs(ids) do
        local values = redis.call('HMGET', 's:' .. S .. ':task:' .. tid, unpack(rank_fields))
        fields[#fields + 1] = tid
        for i = 1, #rank_fields do
          fields[#fields + 1] = values[i] or ''
        end
      end
      out[#out + 1] = { S, open, fields }
    end
  end
  return out
end

-- take_n: args friend, n, strict, actor, idem, then one group of five per
-- candidate in take order: sprint, id, attempt, token, token_sha (the same
-- values ns_task_take takes). Each candidate goes through CL.take, the one
-- guarded take, until n are claimed. A DOWN or FULL answer stops the batch;
-- strict ('1', task take --id) also stops on BLOCKED. The reply lists every
-- answer but NONE and NOTFOUND: a CLAIMED entry is ns_task_take's reply, any
-- other is { status, sprint, id, detail }. A RETRY (the attempt moved since
-- the view) is left for the Go side to take once more on its own.
local function take_n(keys, args)
  local friend, limit = args[1], tonumber(args[2]) or 0
  local strict, actor, idem = args[3] == '1', args[4], args[5]
  local req_sprint, req_id = args[6] or '', args[7] or ''
  
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return {{ 'NOTFRIEND' }}
  end
  
  local out, claimed = {}, 0
  
  local desired = tonumber(redis.call('HGET', 'friend:' .. friend .. ':desired', 'slots') or '0') or 0
  if desired <= 0 then return out end
  local starting = tonumber(redis.call('ZCARD', 'friend:' .. friend .. ':starting') or '0') or 0
  local living = tonumber(redis.call('ZCARD', 'friend:' .. friend .. ':living') or '0') or 0
  local free = desired - starting - living
  if free <= 0 then return out end
  
  if limit == 0 or limit > free then limit = free end
  local n_randoms = #args - 7
  if limit > n_randoms then limit = n_randoms end

  local sprints = { req_sprint }
  if req_sprint == '' then
    sprints = redis.call('ZRANGE', 'sprint:order', 0, -1)
  end

  for _, S in ipairs(sprints) do
    if claimed >= limit then break end
    if S ~= '' and redis.call('HGET', 's:' .. S, 'status') == 'open' then
      local ids = {}
      if req_id ~= '' then
        ids[1] = req_id
      else
        local open = redis.call('ZRANGE', 's:' .. S .. ':open:' .. friend, 0, -1, 'WITHSCORES')
        local tasks = {}
        for i = 1, #open, 2 do
          local tid = open[i]
          local score = tonumber(open[i+1])
          local vals = redis.call('HMGET', 's:' .. S .. ':task:' .. tid, 'est', 'pushed_at')
          local est = tonumber(vals[1]) or 0
          local pushed_at = tonumber(vals[2]) or 0
          local front = false
          if score < 0 then front = true end
          tasks[#tasks+1] = {id=tid, score=score, est=est, pushed_at=pushed_at, front=front}
        end
        table.sort(tasks, function(a, b)
          if a.front ~= b.front then return a.front end
          if a.est ~= b.est then return a.est > b.est end
          if a.score ~= b.score then return a.score < b.score end
          if a.pushed_at ~= b.pushed_at then return a.pushed_at < b.pushed_at end
          return a.id < b.id
        end)
        for _, t in ipairs(tasks) do
          ids[#ids+1] = t.id
        end
      end
      
      for _, tid in ipairs(ids) do
        if claimed >= limit then break end
        
        local attempt = tonumber(redis.call('HGET', 's:' .. S .. ':task:' .. tid, 'attempt') or '0') or 0
        local next_attempt = attempt + 1
        local random_str = args[7 + claimed + 1]
        local token = tostring(next_attempt) .. '.' .. random_str
        local token_sha = string.sub(redis.sha1hex(token), 1, 12)
        
        local reply = CL.take(nil, { S, tid, friend, tostring(next_attempt), token, token_sha, actor, idem })
        local status = reply[1]
        
        if status == 'CLAIMED' then
          claimed = claimed + 1
          out[#out + 1] = reply
        elseif status ~= 'NONE' and status ~= 'NOTFOUND' then
          out[#out + 1] = { status, S, tid, reply[2] or '' }
          if status == 'DOWN' or status == 'FULL' or (strict and status == 'BLOCKED') then
            break
          end
        end
      end
      if out[#out] and (out[#out][1] == 'DOWN' or out[#out][1] == 'FULL') then
        break
      end
    end
  end
  return out
end

redis.register_function{ function_name = 'ns_task_take_view', callback = take_view,
  flags = { 'no-writes' } }
redis.register_function('ns_task_take_n', take_n)
