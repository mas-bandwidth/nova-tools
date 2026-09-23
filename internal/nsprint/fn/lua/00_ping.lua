-- Each file registers its own functions into the library assembled by loader.go.
redis.register_function('ns_ping', function(keys, args)
  return 'PONG'
end)
