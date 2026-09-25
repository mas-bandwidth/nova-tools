// Package land provides the lander service state contract, record model,
// and decision logic over Redis and git (Issue #3139 rev 7).
//
// The unit record contract (retiring PR keys in favor of unit keys per repo and base, §2.2):
//
//	s:<S>:u:<unit>                 hash   repo, base, branch, head, base_sha, stack_parent (sexp edge),
//	                                      files, paths_hash, security, class, state, batch, gate, alone,
//	                                      carried, drop_key (<head8>:<rec_seq>), drop_reason, landed_head,
//	                                      merge_sha, pr (optional); reap fields (nova-tools#3091, read by
//	                                      internal/nsprint/pr): paths, card_type, cut_at write-once by
//	                                      ns_unit_head from the card named by its arg 15 (paths canonical
//	                                      JSON, card_type the TYPE: line, cut_at Redis TIME at
//	                                      ns_card_push); last_read_at, approve_head (+ approve_seq)
//	                                      present-empty on ns_unit_head's create path, then ns_read on a
//	                                      counted read / APPROVE; merged_at present-empty, then ns_land
//	                                      with state=landed, once
//	s:<S>:units                    set    every unit id the sprint knows (ns_unit_head SADDs; the index
//	                                      land status reads, so nothing is SCANned)
//	s:<S>:prunit:<repo>:<n>        string the unit whose card names PR <n>, so why <repo>#<n> resolves
//	                                      without a scan
//	s:<S>:unresolved               hash   HSETNX only: <unit>:<field>-changed:<seq> when ns_unit_head
//	                                      meets a write-once reap field with a different value
//	s:<S>:landable:<repo>:<base>   zset   units, score tier*1e12 + first rec:seq: oldest first within
//	                                      a priority tier (owner ruling); front negative
//	rec:seq                        counter one global sequence stamped on every read, hold, release,
//	                                      head, and intent record
//	s:<S>:read:<unit>:<who>        hash   seq, head, verdict, score, kind, files, done_when, at
//	s:<S>:hold:<unit>:<holder>     hash   seq, head, kind, reason, url, files, done_when, origin,
//	                                      post_land, at, released_by, release_kind, release_reason,
//	                                      release_url, released_at, release_seq
//	land:<repo>:<base>:policy      hash   policy_id, required_set_id, runner_id, at
//	ci:<repo>:<head>:<gid>         hash   write-once: gid = sha256("kind=single", base, base_sha,
//	                                      required_set_id, policy_id, runner_id)[:16];
//	                                      verdict (OK, FAIL), kind, base, base_sha, required_set_id,
//	                                      policy_id, runner_id, receipt, bench, pkg, test, at
//	ci:<repo>:<base>:tip:<tip>:<gid> hash write-once: same fields with kind=tip, base_sha=<tip>
//	ci:<repo>:<head>:gids          set    every gid gated for that head (why stale vs missing)
//	land:<repo>:<base>:batch:<id>  hash   seq, parent, from_tip, members (unit@head), paths, class,
//	                                      state, attempt, token, entry_id, input_id, receipt, reason,
//	                                      created_at
//	land:<repo>:<base>:chain       zset   batch ids by seq
//	land:<repo>:gates              stream group workers: {base, batch, attempt, token}
//	land:<repo>:receipt:<batch>:<attempt> hash write-once: bench, worker, token, from_tip, train_head,
//	                                      train_tree, input_id, selection, steps, verdict (GREEN,
//	                                      RED, ERROR, CONFLICT), failing, flaky_rerun, core_s, at
//	land:<repo>:<base>:tip         hash   sha, at, by (publisher or fetch)
//	land:<repo>:<base>:lease       string publisher lease <gen>:<token> (SET NX PX 6000, renewed every 2s)
//	land:<repo>:<base>:writer      hash   gen, owner (old-loop or nova-sprint), since, by
//	land:<repo>:<base>:pub:<batch> hash   state (intent, pushed, verified, dead), from_tip, train_head,
//	                                      train_tree, gen, policy_id, rec_seq_cut, at per state
//	landed:<repo>:<unit>:<head>    string SET NX: <merge_sha> <batch> <receipt>
//	land:<repo>:<base>:freeze      hash   reason, remedy, at
//	land:<repo>:events             stream, never trimmed (#3878): one entry per transition
//	friend:<f>:state               hash   state (up|underfull|idle|out-of-credits|down|away), since
//
// Fences and Linearization Point (§2.3):
//   - Gate fence: token = INCR land:<repo>:tok. ns_gate_claim succeeds only for the batch's current
//     attempt in state queued; later functions compare tokens and return STALE on mismatch.
//   - Publisher fence: lease is <gen>:<token>; gen must match land:<repo>:<base>:writer with owner
//     nova-sprint. Checked by ns_batch_plan, ns_land_intent, and ns_land.
//   - Linearization point: ns_land_intent. In one atomic call: verifies lease/writer gen, no
//     unresolved pub on this base, receipt is GREEN and from_tip matches tip record, all members
//     green at gated head with holds_open = 0, current policy matches, inbound consumer fresh.
//     Writes pub:<batch> state=intent rec_seq_cut=<seq> and sets members landing.
//     Holds after rec_seq_cut receive post_land=1 and enqueue follow-up tasks to q:<author> and q:<holder>.
//   - Double landing protection: ns_land writes landed:<repo>:<unit>:<head> (SET NX). A second call
//     writes nothing and returns ALREADY.
//
// Unit States (§2.4):
//
//	opened -> reading -> landable -> batched -> gating -> green -> landing -> landed
//	(exits: dropped, settled; landed is terminal).
package land
