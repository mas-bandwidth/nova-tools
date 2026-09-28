redis.register_function('ns_health', function(keys, args)
  return #keys + #args
end)
