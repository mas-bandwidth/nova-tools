# nova-reserve

Let jobs wait their turn on a busy machine. A **slot** is one place in the
concurrent job limit. Jobs using the same reservation directory share that
limit. An exclusive reservation waits for the others to finish, then holds
the whole capacity.

```sh
nova-reserve set --slots 4
nova-reserve --wait 15m --why "unit tests" -- go test ./...
nova-reserve status
```

Set capacity while no job holds a reservation. It is stored for later callers;
`--slots` on a run checks that value. Use `hold --for 2h` for a reservation
without a child command; without `--for`, it releases when its parent exits.
Use `--exclusive` to reserve the whole machine.

Read the [command reference](../docs/SPEC-RESERVE.md) for flags, exit codes,
lock files, platform limits and build commands. Run `nova-reserve help` for
the command summary.
