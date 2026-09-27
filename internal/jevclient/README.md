# Typed provider transport

`jevclient` sends a typed question set to TypeSafe Jev, validates the returned
answers and preserves whether each usage counter was reported. It uses only
the Go standard library. It has no decision policy, journal, Redis, SQLite or
sprint dependency. `internal/decide` wraps it with constraints and recording;
`nova-bus` uses it directly for optional public-inbox classification.

The existing `decide:` error prefix and question/answer wire remain unchanged.
Missing answers remain absent; callers keep their existing unknown-answer
policy. The client supplies no authorization policy or durable state machine.
Its ten-second request deadline remains in place.

`New` reads only the named key environment variable and its documented fallback.
The bus constructs no client for private evidence: its separate private rules
path and public-marker admission are unchanged. Shared redaction still comes
from `internal/decide/questions`, which itself has only standard-library imports.

## Validation

All tests in this module run in parallel, with fake transports or local HTTP
servers. Moved transport tests retain their original assertions; injected
environment lookup removes three old serial-test exceptions. Generated tests
check closed choice sets and lossless numeric journal values. Concurrent calls
must return the answer to their own request. Refusal cases cover invalid
questions, credential selection, invalid URLs, cancellation, response reads,
HTTP failures, malformed responses and unoffered answers; bodies are closed
and known usage survives a post-call refusal.

On the extraction tree, the module suite ran in 0.130 seconds, with 97.0%
statement coverage. Including the unchanged `internal/decide` consumer tests
gave 99.3% across this module. Those consumer tests cover the environment-reading
`New` entry point and additional answer-validation branches. The sole uncovered
statement in the combined profile is the defensive JSON-encoding error return
in `newRequest`: its constructed payload contains only strings, maps of strings,
and slices of strings, so valid public inputs cannot reach an encoding error.
The return stays defensive; no test-only failure hook was added.

The shared client also passes the race detector. The complete bus functional
suite passed in 17.377 seconds, including both public classification and private
zero-provider-call tests. `TestBusHasNoStorageOrSprintDependencies` walks all
platforms' production imports transitively and rejects a reintroduced storage,
sprint-policy or third-party dependency. The provider transport is held to
standard-library imports only.

To reproduce coverage:

```sh
go test -p 2 ./internal/jevclient ./internal/decide \
  -coverpkg=./internal/jevclient -coverprofile=coverage.out -count=1
go tool cover -func=coverage.out
go list -deps ./cmd/nova-bus
```

These are module and consumer checks, not a claim that every module in
nova-tools has met the new testing bar.
