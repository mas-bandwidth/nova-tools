#!lua name=testlib
local function test(keys, args)
  return redis.sha256hex("hello")
end
redis.register_function('test', test)
