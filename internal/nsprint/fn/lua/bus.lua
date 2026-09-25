-- bus.lua
local function post(stream, data)
  return redis.call('XADD', stream, '*', 'data', data)
end
return { post = post }
