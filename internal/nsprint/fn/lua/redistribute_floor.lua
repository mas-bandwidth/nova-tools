-- Atomic floor redistribution (#3103). args = sprint, floor, max-move,
-- actor, idem. The complete plan and its writes happen in this FCALL.

local FR, RD = NS.friend_roles, NS.redistribute
local fr_actor, fr_has_role, fr_roster = FR.fr_actor, FR.fr_has_role, FR.fr_roster
local rd_caplog, rd_carried_hold, rd_free = RD.rd_caplog, RD.rd_carried_hold, RD.rd_free
local rd_now_ms, rd_open_sprints, rd_route = RD.rd_now_ms, RD.rd_open_sprints, RD.rd_route

local function redistribute_floor(keys, args)
  local S, floor, maxmove = args[1], tonumber(args[2]), tonumber(args[3])
  local actor, idem = args[4], args[5]
  local actor_err = fr_actor(actor)
  if actor_err then return actor_err end
  local roster = fr_roster()
  if not roster then return redis.error_reply('ERR NOROSTER') end
  if not S or S == '' or not floor or floor < 0 or not maxmove or maxmove < 0 then
    return { 'INVALID' }
  end
  local status = redis.call('HGET', 's:' .. S, 'status')
  if status ~= 'open' and status ~= 'paused' then return { 'NOSPRINT' } end
  local at, moved, events = rd_now_ms(), 0, {}
  local sprints, free, woken, held = rd_open_sprints(), {}, {}, {}
  for _, friend in ipairs(roster.friends) do free[friend] = rd_free(friend, sprints) end

  for _, receiver in ipairs(roster.friends) do
    local rkey = 's:' .. S .. ':open:' .. receiver
    while moved < maxmove and redis.call('ZCARD', rkey) < floor and free[receiver] ~= nil and free[receiver] > 0 do
      local donors = {}
      for _, donor in ipairs(roster.friends) do
        if donor ~= receiver then
          donors[#donors + 1] = { friend = donor, count = redis.call('ZCARD', 's:' .. S .. ':open:' .. donor) }
        end
      end
      table.sort(donors, function(a, b)
        if a.count == b.count then return a.friend < b.friend end
        return a.count > b.count
      end)
      local did_move = false
      for _, donor in ipairs(donors) do
        if donor.count > floor then
          local items = redis.call('ZRANGE', 's:' .. S .. ':open:' .. donor.friend, 0, -1, 'WITHSCORES')
          for i = 1, #items, 2 do
            local id = items[i]
            local key = 'task:' .. id
            local kind = redis.call('HGET', key, 'kind') or 'work'
            local eligible = (kind == 'fix' and (fr_has_role(receiver, 'builder') or fr_has_role(receiver, 'coordinator'))) or
              ((kind == 'read' or kind == 'review') and fr_has_role(receiver, 'may-hold') and
                not rd_carried_hold(S, donor.friend, key))
            if eligible then
              local ctx = { f = donor.friend, why = 'floor ' .. tostring(floor),
                marker = '[moved from ' .. donor.friend .. ': floor ' .. tostring(floor) .. ']',
                mayhold = roster.mayhold, builders = roster.builders, coord = roster.coordinator,
                free = free, woken = woken, sprints = sprints, actor = actor, idem = idem,
                at = at, held = held, moved = 0, leases = 0, released = 0, unrouted = 0,
                kept = 0, events = events, to = receiver, to_list = { receiver },
                log_kind = 'task assign' }
              rd_route(ctx, S, id, tonumber(items[i + 1]) or 0, false)
              if ctx.moved == 1 then
                if free[donor.friend] ~= nil then free[donor.friend] = free[donor.friend] + 1 end
                moved, did_move = moved + 1, true
                break
              end
            end
          end
        end
        if did_move then break end
      end
      if not did_move then break end
    end
  end
  local targets = {}
  for friend in pairs(woken) do targets[#targets + 1] = friend end
  table.sort(targets)
  for _, friend in ipairs(targets) do
    local wake = 'friend:' .. friend .. ':wake'
    redis.call('LPUSH', wake, tostring(at) .. ':floor')
    redis.call('LTRIM', wake, 0, 0)
    rd_caplog('friend-wake', friend, 'floor', actor, idem, at)
  end
  return { 'OK', tostring(moved), events }
end

redis.register_function('ns_redistribute_floor', redistribute_floor)
