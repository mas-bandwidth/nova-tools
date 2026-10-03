REPO: example/tools
PATHS: internal/greet/greet_test.go
TEST: internal/greet TestGreetNamesTheReader

THE TASK. Every listed test assertion is written as a negation and is to be written as the
assertion that names what it pins, with the same meaning: `assert.False(t, a != b, msg...)`
becomes `assert.Equal(t, b, a, msg...)`. Change nothing else. The lines:
internal/greet/greet_test.go:14  assert.False(t, got != "hello, reader", "the greeting")
