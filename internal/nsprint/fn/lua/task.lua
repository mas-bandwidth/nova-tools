-- task_live: the path-lint snapshot for task push (nova-tools #3067). One
-- read-only call returns whether the pushed id already exists (the push
-- function then answers EXISTS, CLOSED or CONFLICT, never the lint), then
-- every live (open, claimed or working) task of every sprint in sprint:order
-- plus the pushing sprint, as flat rows of sprint, id, kind, repo, title. The
-- state indexes are only candidates: a row is returned only when its hash
-- says the task is live. Nothing is written.
local function task_live(keys, args)
  local push_sprint, push_id = args[1], args[2]
  local sprints = redis.call('ZRANGE', 'sprint:order', 0, -1)
  local seen = {}
  local order = {}
  for _, s in ipairs(sprints) do
    if not seen[s] then
      seen[s] = true
      order[#order + 1] = s
    end
  end
  if push_sprint ~= '' and not seen[push_sprint] then
    order[#order + 1] = push_sprint
  end
  local out = { tostring(redis.call('EXISTS', 's:' .. push_sprint .. ':task:' .. push_id)) }
  local live = { open = true, claimed = true, working = true }
  for _, s in ipairs(order) do
    local done = {}
    for _, idx in ipairs({ 'open', 'claimed', 'working' }) do
      for _, id in ipairs(redis.call('SMEMBERS', 's:' .. s .. ':idx:task:' .. idx)) do
        if not done[id] then
          done[id] = true
          local row = redis.call('HMGET', 's:' .. s .. ':task:' .. id, 'state', 'kind', 'repo', 'title')
          if row[1] and live[row[1]] then
            out[#out + 1] = s
            out[#out + 1] = id
            out[#out + 1] = row[2] or ''
            out[#out + 1] = row[3] or ''
            out[#out + 1] = row[4] or ''
          end
        end
      end
    end
  end
  return out
end

redis.register_function{ function_name = 'ns_task_live', callback = task_live,
  flags = { 'no-writes' } }
