-- ns_card_push lua script
local key = KEYS[1]
local kind = ARGV[1]
local base_sha = ARGV[2]
local check = ARGV[3]
local expect = ARGV[4]
local check_sha = ARGV[5]
local expect_sha = ARGV[6]

redis.call('HSET', key, 'kind', kind, 'base_sha', base_sha, 'check', check, 'expect', expect, 'check_sha256', check_sha, 'expect_sha256', expect_sha)
return 1
