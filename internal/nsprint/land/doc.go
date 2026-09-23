// Package land answers "why is this approved PR not landed" and "what is the
// lander doing" from the PR record alone (#2756 v6 4.9; nova-tools #3106).
// It reads Redis and never GitHub, and it writes nothing.
//
// The record contract. These are the minimal keys and fields `why` and
// `land status` read. #3091 (ns_pr_eval, the landable set) and #3092 (holds
// and releases as records, ns_ingest_disposition) MUST produce them in these
// shapes; a field they do not write prints MISSING here, never a pass.
//
//	s:<S>:prs                  set   every PR id <repo>#<n> the sprint knows (ns_pr_eval SADDs; the
//	                                 index land status reads, so nothing is SCANned)
//	s:<S>:pr:<repo>:<n>        hash  head, author (friend name), draft (true|false), mergeable,
//	                                 stack_parent (none | #<n> | <repo>#<n>), land_bar,
//	                                 state (opened|reading|landable|landing|landed|dropped|closed),
//	                                 lane, lane_at, drop_key, drop_reason, merge_sha,
//	                                 latest_comment_id, latest_record_id
//	s:<S>:landable             zset  <repo>#<n>, score = priority (front negative)
//	s:<S>:disp:<repo>:<n>      hash  <friend>@<head> -> "<verdict> <score> <url> <comment_id>"
//	s:<S>:hold:<repo>:<n>      hash  h<k> -> JSON {holder, head, kind, reason, url, at,
//	                                 released_by, release_kind, release_url, released_at};
//	                                 open while released_by is empty
//	s:<S>:policy               hash  readers (default 1), absent_after (Go duration, default 60m)
//	ci:<repo>:<head>           hash  verdict (PENDING|OK|FAIL|FLAKY); absent is MISSING
//	friend:<f>:state           hash  state (up|underfull|idle|out-of-credits|down|away), since
//
// Times (since, lane_at, at) are unix seconds or RFC 3339. drop_key is
// "<head>:<latest_comment_id>:<latest_record_id>" as it was when the lane
// dropped the PR; `why` compares it with the same three fields now.
//
// Each verb takes at most three pipelined round trips (redis-in-batches).
package land
