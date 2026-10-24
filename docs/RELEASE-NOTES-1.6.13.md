The stable tools point release after v1.6.12. A friend's hook now takes her ready cards by itself, and says where its challenges go. A member's push refuses a result head read from a stale RESULT.md that the repository carries. nova-sprint is released on its own line.

Install: download the assets for your platform from this release, then run `nova-update release install --from <release root> --version v1.6.13 --bin <dir>` with the nova-sprint release staged beside them (see docs/SPEC-RELEASE.md). nova-hook and nova-swarm changed. A friend's running hook takes the new behaviour only when it is restarted.

## What changed in v1.6.13

- **A friend's hook takes her ready cards (#6084).** Before this, only the supervised hook service ran the friend lane client. A friend hooked from her own session (`nova-hook hook --as <name> --role friend`) took nothing, and nothing had replaced the retired friend daemons' take.
  - Every friend hook, in a session or as a service, now asks the sprint server every 45 s. The interval was a third of the 10-minute lane lease.
  - Each round reads the server's own record of her working cards and takes into her free lanes, up to her nova-config width, with the fleet's lane take. The server holds the width, deals only her row's cards, and deals in order.
  - The hook keeps no lane table, and it asks only while her record is proven with the heartbeat server's evidence.
  - Each card newly taken is one line on the hook's stream: `HOOK TAKEN card=<card>@<gen> brief=<path> lane=<n> epoch=<e> as=friend.<name>`, with a NOTE naming the verb that finishes it.
  - A hook with no harness route sends no bus notice, because its stream is the route. A hook with a harness route still sends the one notice. The hook never does a card's work.
  - The model is tla/FriendAutoTake.tla. It checks TakesWithinWidth, NeverOthersCard, DealOrder and TakenWithinInterval, with four reversed witnesses.
- **A friend's hook says its route at arming (#6084).**
  - With no harness route, the hook prints `HOOK STATE state=route-stdout`: its challenges and TAKEN lines reach the model only through its stdout, and `--harness <name>` delivers into the harness instead.
  - It adds `HOOK WARN route=unread` when no session can be reading that stdout: a regular file, the null device, or a parent that has exited.
- **A stale RESULT.md is not a card's result (#6084).** A member's push refuses the result when two things hold: the result lookup found it at the checkout's `repo/RESULT.md`, and that file is tracked and unchanged at the staged commit. The refusal line names the file and says it is a stale file from the repository. Before this, a RESULT.md committed to a repository's main named an old head, and every finish on that repository that wrote no result of its own was refused as a non-descendant.
- Security read for the sensitive paths in this range (pkg/secrets and cmd/nova-secrets, from #6062 and the unit-budget change): https://github.com/mas-bandwidth/nova-tools/pull/6062#issuecomment-6388901296.
