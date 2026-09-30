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
// (TestACLSeatsRunTheCardVerbs). Every reader of a table set keys it by the
// sprint epoch (nova-tools#4238): the consumer, table and coordinator seats
// read sprint:epoch (the friend seat's ~sprint:* holds it), the friend seat's
// take and width count their working set through ns_cell_zcard, its beat
// reads the copies it holds through ns_cell_zrange, and the
// coordinator's lander reads its members through ns_ws_zrange under ~ws:*,
// as the live row has it (TestACLSeatsReadTheSprintEpoch). The friend seat
// also keeps its own beat (seat-keeps-beat: friend beat's HSET and PERSIST of
// friend:<f>:beat) and its one beat loop's lease and models through the
// ns_friend_loop_* and ns_friend_models functions, never SET or EVAL
// (TestFriendSeatKeepsBeatUnderACL). The tables of internal/ntable
// (nova-table; the sprint's streams table) live under ~table:* with the
// registry set tables: the coordinator seat writes them (the plain writes
// plus the two functions, ns_oset_move and ns_table_clear;
// internal/ntable's TestTableGrantsAreExactlyWhatTheWriterNeeds runs every
// write as a user holding exactly those) and the table seat reads them.
// Layer 1's lifecycle, ns_tset_define and ns_tset_teardown (the L1 contract
// amendment, lifecycle, 2026-09-30), is the coordinator seat's alone, over
// the new path's namespace ~{sprint}:*, whose keys both write through the
// library (TestACLEachActorCallsOnlyItsFunctions). A table's read set and batch check
// their members' places with one ZMSCORE per cell, and a sprint's twin
// catches a table up from its change stream with XREVRANGE (nova-sprint's
// tick, twin.go): the coordinator and bench seats hold both.
var ACLRules = []string{
	"ns-friend resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~benches ~friends ~friends:* ~task:* ~ws:* ~q:* ~sprint:* ~pr:* ~ref:* ~consumers ~readers %R~cfg:card +del +exists +hdel +hget +hgetall +hmget +hset +hincrby +lrange +lpos +pexpire +persist +pttl +rpush +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall_ro|ns_cell_zcard +fcall_ro|ns_cell_zrange +fcall|ns_ping +fcall|ns_health +fcall|ns_cm_work +fcall|ns_cm_beat +fcall|ns_cm_end +fcall|ns_cm_cancel +fcall|ns_task_take +fcall|ns_task_beat +fcall|ns_task_done +fcall|ns_friend_loop_claim +fcall|ns_friend_loop_renew +fcall|ns_friend_loop_release +fcall|ns_friend_models +fcall|ns_cm_owner (~friend:*:wake +blpop)",
	"ns-bench resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~benches ~friends ~friends:* ~task:* ~ws:* ~q:* ~sprint:* ~pr:* ~ref:* ~consumers ~readers %R~cfg:card +del +exists +hdel +hget +hgetall +hmget +hset +hincrby +hexists +lrange +lpos +pexpire +pttl +rpush +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +zmscore +xrevrange +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall|ns_ping +fcall|ns_health +fcall|ns_cm_work +fcall|ns_cm_beat +fcall|ns_cm_end +fcall|ns_cm_cancel +fcall|ns_bench_beat +fcall|ns_bench_release (+get +set ~bench:*:owner)",
	"ns-reconciler resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~benches ~friends ~friends:* +del +exists +hget +hgetall +hmget +hset +pexpire +pttl +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall|ns_ping +fcall|ns_health +fcall|ns_reconciler_acquire +fcall|ns_reconciler_renew +fcall|ns_reconciler_pass +fcall|ns_reconciler_release +fcall|ns_task_expire (~s:*:log ~cap:log +xreadgroup +xautoclaim +xpending)",
	"ns-consumer resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~sprint:epoch ~benches ~friends ~friends:* +del +exists +hget +hgetall +hmget +hset +pexpire +pttl +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall|ns_ping +fcall|ns_health +fcall|ns_task_push (~s:*:log ~cap:log +xreadgroup +xautoclaim +xpending)",
	"ns-coordinator resetkeys resetchannels -@all +ping +client|setname +client|id ~s:* ~bench:* ~friend:* ~machine:* ~lease:* ~proc:* ~cap:* ~ci:* ~sprints ~sprint:order ~sprint:epoch ~ws:* ~benches ~friends ~friends:* ~table:* ~tables ~task:* ~view:* ~views +hdel +type +xinfo|stream +del +exists +hget +hgetall +hmget +hlen +hstrlen +hexists +hset +pexpire +pttl +sadd +scard +sismember +smembers +smove +srem +time +xack +xadd +zadd +zcard +zcount +zrange +zrem +zscore +zmscore +xrevrange +fcall_ro|ns_snapshot +fcall_ro|ns_task_live +fcall_ro|ns_ws_zrange +fcall|ns_ping +fcall|ns_health +fcall|ns_task_push +fcall|ns_task_cancel +fcall|ns_capacity_desired +fcall|ns_capacity_machine +fcall|ns_oset_move +fcall|ns_table_clear +rename +fcall|ns_table_set +fcall|ns_table_rows_add +fcall|ns_table_rows_hide +fcall|ns_view_set +fcall|ns_view_state +fcall|ns_view_del +fcall|ns_table_row_set +fcall|ns_table_create +fcall|ns_table_drop +fcall|ns_table_drop_definition +fcall|ns_table_member_create +fcall|ns_table_row_add +fcall|ns_table_row_del +fcall|ns_table_cell_add +fcall|ns_table_cell_remove +fcall|ns_table_cell_move +fcall|ns_table_bind +fcall|ns_table_apply +fcall_ro|ns_table_read +fcall_ro|ns_table_read_set +fcall_ro|ns_table_check +scan +fcall_ro|ns_table_list +fcall_ro|ns_view_get +fcall_ro|ns_view_list +fcall_ro|ns_table_member_find +fcall_ro|ns_table_members ~{sprint}:* +fcall|ns_tset_define +fcall|ns_tset_teardown",
	"ns-table resetkeys resetchannels -@all +ping +client|setname +client|id %R~s:* %R~bench:* %R~friend:* %R~machine:* %R~lease:* %R~proc:* %R~cap:* %R~ci:* %R~sprints %R~sprint:order %R~sprint:epoch %R~benches %R~friends %R~table:* %R~tables %R~view:* %R~views +exists +hget +hgetall +hmget +pttl +scard +sismember +smembers +time +zcard +zcount +zrange +zscore +fcall_ro|ns_snapshot +fcall_ro|ns_table_read +fcall_ro|ns_table_read_set +fcall_ro|ns_table_list +fcall_ro|ns_view_get +fcall_ro|ns_view_list +fcall_ro|ns_table_member_find +fcall_ro|ns_table_members",
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
