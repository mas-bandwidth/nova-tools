package store

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// ACLRules are the baseline permissions for each actor in the fleet Redis.
// The bench and friend seats run consumer copies through the table moves
// (#3998: card work --fill, card beat, card end, and the session's give-back,
// card cancel), whose moves read and write task:*, ws:*, q:*, sprint:*, pr:*
// (a read's line on the PR record), consumers and readers, with HDEL, LPOS and
// RPUSH; the bench's copy ledger reads cfg:card
// (TestACLSeatsRunTheCardVerbs). The friend seat also keeps its own beat
// (seat-keeps-beat: friend beat's HSET and PERSIST of friend:<f>:beat) and
// its one beat loop's lease and models through the ns_friend_loop_* and
// ns_friend_models functions, never SET or EVAL (TestFriendSeatKeepsBeatUnderACL).
var ACLRules = []string{
	"ns-friend resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~benches ~friends ~friends:* ~task:* ~ws:* ~q:* ~sprint:* ~pr:* ~ref:* ~consumers ~readers %R~cfg:card +del +exists +hdel +hget +hgetall +hmget +hset +hincrby +lpos +pexpire +persist +pttl +rpush +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall|ns_ping +fcall|ns_health +fcall|ns_cm_work +fcall|ns_cm_beat +fcall|ns_cm_end +fcall|ns_cm_cancel +fcall|ns_task_take +fcall|ns_task_beat +fcall|ns_task_done +fcall|ns_friend_loop_claim +fcall|ns_friend_loop_renew +fcall|ns_friend_loop_release +fcall|ns_friend_models +fcall|ns_cm_owner (~friend:*:wake +blpop)",
	"ns-bench resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~benches ~friends ~friends:* ~task:* ~ws:* ~q:* ~sprint:* ~pr:* ~ref:* ~consumers ~readers %R~cfg:card +del +exists +hdel +hget +hgetall +hmget +hset +hincrby +hexists +lpos +pexpire +pttl +rpush +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall|ns_ping +fcall|ns_health +fcall|ns_cm_work +fcall|ns_cm_beat +fcall|ns_cm_end +fcall|ns_cm_cancel +fcall|ns_bench_beat +fcall|ns_bench_release (+get +set ~bench:*:owner)",
	"ns-reconciler resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~benches ~friends ~friends:* +del +exists +hget +hgetall +hmget +hset +pexpire +pttl +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall|ns_ping +fcall|ns_health +fcall|ns_reconciler_acquire +fcall|ns_reconciler_renew +fcall|ns_reconciler_pass +fcall|ns_reconciler_release +fcall|ns_task_expire (~s:*:log ~cap:log +xreadgroup +xautoclaim +xpending)",
	"ns-consumer resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~benches ~friends ~friends:* +del +exists +hget +hgetall +hmget +hset +pexpire +pttl +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall|ns_ping +fcall|ns_health +fcall|ns_task_push (~s:*:log ~cap:log +xreadgroup +xautoclaim +xpending)",
	"ns-coordinator resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~benches ~friends ~friends:* +del +exists +hget +hgetall +hmget +hset +pexpire +pttl +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall|ns_ping +fcall|ns_health +fcall|ns_task_push +fcall|ns_task_cancel +fcall|ns_capacity_desired +fcall|ns_capacity_machine",
	"ns-table resetkeys resetchannels -@all +ping +client|setname +client|id %R~s:* %R~bench:* %R~friend:* %R~machine:* %R~lease:* %R~proc:* %R~cap:* %R~ci:* %R~sprints %R~sprint:order %R~benches %R~friends +exists +hget +hgetall +hmget +pttl +scard +sismember +smembers +time +zcard +zcount +zrange +zscore +fcall_ro|ns_snapshot",
	"ns-deploy resetkeys resetchannels -@all +ping +client|setname +client|id +function|load +function|list",
}

// DeployACLs installs the per-actor ACL users on the store. It leaves their
// passwords unchanged.
func DeployACLs(ctx context.Context, client *redis.Client) error {
	for _, rule := range ACLRules {
		parts := aclArgs(rule)
		name := parts[0]
		args := []any{"ACL", "SETUSER", name, "on"}
		for _, p := range parts[1:] {
			args = append(args, p)
		}
		if err := client.Do(ctx, args...).Err(); err != nil {
			return err
		}
	}
	return nil
}

// aclArgs splits a rule on spaces, keeping a parenthesised selector
// ("(+get +set ~bench:*:owner)") as ONE argument: ACL SETUSER takes a
// selector whole, and a seat's direct-write scope is what a selector is for.
func aclArgs(rule string) []string {
	var out []string
	depth := 0
	start := 0
	for i, r := range rule {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ' ':
			if depth == 0 {
				if i > start {
					out = append(out, rule[start:i])
				}
				start = i + 1
			}
		}
	}
	if start < len(rule) {
		out = append(out, rule[start:])
	}
	return out
}
