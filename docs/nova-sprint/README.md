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
directory beneath the test's temporary directory. When `redis-server` is absent
from a CI runner, the integration controls skip there; they run on a Redis bench.
