REPO: example/tools
BASE: main
PATHS: internal/greet/greet_test.go
TEST: internal/greet TestGreetNamesTheReader

THE TASK. One assertion is written as a negation and is to be written as the assertion that
names what it pins, with the same meaning. Change nothing else.

STEP 1. In internal/greet/greet_test.go, line 14 reads
  assert.False(t, got != "hello, reader", "the greeting")
Make it
  assert.Equal(t, "hello, reader", got, "the greeting")
STEP 2. Run the gate: `gofmt -l internal/greet` (prints nothing) and
`go test -count=1 -timeout 600s ./internal/greet/` (ok).
STEP 3. Commit with the message `greet: the greeting test names what it pins`.
STEP 4. End with `gh pr create --title "greet: the greeting test names what it pins"`; the body
gives the diff stat and the gate's last lines.
