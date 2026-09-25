import re
with open('internal/nsprint/fn/lua/task_take.lua', 'r') as f:
    lua_code = f.read()

new_take_n = """local function take_n(keys, args)
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
"""

start_idx = lua_code.find('local function take_n(keys, args)')
end_idx = lua_code.find('end\n\nredis.register_function', start_idx)

final_code = lua_code[:start_idx] + new_take_n + lua_code[end_idx:]
with open('internal/nsprint/fn/lua/task_take.lua', 'w') as f:
    f.write(final_code)
