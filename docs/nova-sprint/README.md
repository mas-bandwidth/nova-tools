# nova-sprint scaffold

`nova-sprint help` lists verbs registered by their own Go files. A new verb
calls `register` from its `init` function and requires no change to `main.go`.

Redis transitions belong in `internal/nsprint/fn/lua/*.lua`. Each file calls
`redis.register_function`; `fn.Source` embeds and concatenates the files into
the `nova_sprint` Redis Function library, and `fn.Load` installs that library
with `FUNCTION LOAD REPLACE`. The initial `ns_ping` function checks deployment.

`store.PipelineHMGet` queues a batch of hash reads before one `Exec`. It is the
read seam for census and other non-snapshot bulk reads. A table snapshot must
use a single read-only Redis Function to retain one consistent instant.

The tests start a throwaway Redis server bound to loopback, with its working
directory beneath the test's temporary directory (`internal/nsprint/testutil`).
CI sets `NOVA_CI=1` and installs `redis-server`, so a missing binary fails the
run instead of skipping it. The process is private. It is not the fleet store.

The fleet Redis has its default user off, so every verb against it needs the
ACL user *and* its password in one pair: `NOVA_SPRINT_REDIS_USER=bench` and
`NOVA_REDIS_BENCH_PASSWORD` (the password reaches the process through
`nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD`, never a flag). A password
with no user is refused naming the missing variable and the pair.

A consumer copy's life on a bench, and the work copy's end that pushes its
branch and opens its PR (#4227), is in [copies.md](copies.md).

The stream block of the live table is a nova-table now (nova-tools
`internal/ntable`, [docs/nova-table/README.md](../nova-table/README.md)):
the table `streams`, one row per stream of `ws:order`, every cell bound to
the `ws:<s>:<state>` set the card model keeps under the sprint epoch, the
sentinel excluded. The tick reads the cells through `ntable.QueueCells` in
its one pipeline (`table.StreamsShape`, the `ws.CellReader` seam of
`ws.CountsReader`, so the headline and the block are still one count),
renders the block through `ntable.Render` (`table.StreamsOf`, byte for byte
the old block), and the loop binds the table in the store (`ntable.Bind`)
whenever its shape moves, so `nova-table render streams --hide-zero-rows`
prints the same block. Nothing about the ws sets or `02_card_move.lua`
changed; a write to a stream cell through nova-table is refused naming
`nova-sprint task move`.
