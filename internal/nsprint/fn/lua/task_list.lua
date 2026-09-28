-- ns_task_list: the single FCALL_RO behind List (nova-tools #3263). One
-- read-only call returns this friend's task rows across all sprints (or one
-- named sprint) as a flat list of sprint, id, state. The index sets are only
-- candidates; the hash is the source of truth. The call applies the same
-- filtering the Go List function did: open tasks must be in the friend's
-- assigned queue, all other live tasks must name the friend as owner, and
-- without a state filter only open, claimed, working and waiting are returned.
-- The record is the one card store task:<id> (#3778): s:<S>:task:<id> is
-- retired, so the sprint's index sets name the ids and task:<id> holds them.

local function is_assigned(queue, id)
  for _, qid in ipairs(queue) do
    if qid == id then return true end
  end
  return false
end

local function task_list(keys, args)
  local as, req_sprint, req_state = args[1], args[2] or '', args[3] or ''
  if as == '' then
    return { 'error', 'as is required' }
  end

  -- Collect sprints.
  local sprints = {}
  if req_sprint ~= '' then
    sprints[1] = req_sprint
  else
    local ordered = redis.call('ZRANGE', 'sprint:order', 0, -1)
    for _, s in ipairs(ordered) do
      sprints[#sprints + 1] = s
    end
  end

  local live = { open = true, claimed = true, working = true, waiting = true }
  local out = {}

  for _, sprint in ipairs(sprints) do
    if sprint == '' then
      -- skip
    else
      -- Read the five index keys for this sprint+friend.
      local queue = redis.call('ZRANGE', 's:' .. sprint .. ':open:' .. as, 0, -1)
      local claimed = redis.call('SMEMBERS', 's:' .. sprint .. ':idx:task:claimed')
      local working = redis.call('SMEMBERS', 's:' .. sprint .. ':idx:task:working')
      local waiting = redis.call('SMEMBERS', 's:' .. sprint .. ':idx:task:waiting')
      local done = redis.call('SMEMBERS', 's:' .. sprint .. ':done:' .. as)

      -- Build the candidate set, tracking assigned status from the open queue.
      local seen = {}
      local ids = {}
      for _, id in ipairs(queue) do
        if id ~= '' and not seen[id] then
          seen[id] = true
          ids[#ids + 1] = id
        end
      end
      for _, list in ipairs({ claimed, working, waiting, done }) do
        for _, id in ipairs(list) do
          if id ~= '' and not seen[id] then
            seen[id] = true
            ids[#ids + 1] = id
          end
        end
      end

      -- HMGET state and owner for each candidate.
      for _, id in ipairs(ids) do
        local row = redis.call('HMGET', 'task:' .. id, 'state', 'owner')
        local state = row[1] or ''
        local owner = row[2] or ''
        if state ~= '' then
          local ok = true
          -- State filter.
          if req_state ~= '' and state ~= req_state then
            ok = false
          elseif req_state == '' and not live[state] then
            ok = false
          -- Open tasks must be assigned (from this friend's queue).
          elseif state == 'open' and not is_assigned(queue, id) then
            ok = false
          -- Non-open tasks must name this friend as owner.
          elseif state ~= 'open' and owner ~= as then
            ok = false
          end
          if ok then
            out[#out + 1] = sprint
            out[#out + 1] = id
            out[#out + 1] = state
          end
        end
      end
    end
  end

  return out
end

redis.register_function{ function_name = 'ns_task_list', callback = task_list,
  flags = { 'no-writes' } }
