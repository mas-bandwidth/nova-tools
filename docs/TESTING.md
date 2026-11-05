# Testing

nova-tools is one Go module. CI (`.github/workflows/ci.yml`) runs exactly the
commands below; run the same ones before you push.

```sh
gofmt -l .                                   # prints nothing
go vet ./...
go vet -tags functional,slow ./...           # the tagged test files compile too
GOOS=windows go vet ./...                    # the Windows build compiles
NOVA_TEST_NO_HOST=1 go test -p 4 ./...       # unit tier
NOVA_TEST_NO_HOST=1 go test -p 4 -tags functional,slow ./...   # functional tier
tla/check.sh /path/to/tla2tools.jar          # TLC over every model case
```

Run the whole tree on a bench, not a working machine.

## The two tiers

- **Unit.** No real time, no sockets, no network, no outside services. Time is
  an argument (`Update(now)`, never `sleep`); the transport is faked; a test
  stays inside its own temporary directory. A flaky unit test is fixed by
  applying these rules.
- **Functional.** Anything that needs the real thing (redis-server, postgres,
  a built binary, a real socket, a child process, a clock) lives in a file
  behind `//go:build functional`. Tests over a real UDP or TCP socket tolerate
  loss and never assert 100% delivery.

Tests are isolated (their own directories, ports and state) so they run in
parallel; nothing runs serially unless it shares state, and then it says why.

## Models

Every state machine has a TLA+ module beside its code, with reversed
witnesses: configurations that must fail, proving the checked property can
break. `tla/check.sh` runs them all in parallel. A `tla/` directory either has a
`CASES.tsv` (config, module, expected, property, deadlock) or holds one module
whose `*Broken*.cfg` files must end in a violation and whose other `.cfg` files
must pass. Where code does index arithmetic (rings, compaction, wrapping
counters), the model carries the same arithmetic, checked at capacity 3 or 4.
