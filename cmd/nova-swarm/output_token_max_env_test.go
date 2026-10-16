package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The harness sets the child's output-token ceiling itself (native.go,
// nativeChildEnvFrom): opencode caps every completion at 32,000 output tokens by
// default, reasoning included, so a card can spend the whole cap thinking and end
// with finish "length" and no result. Until now the lift lived only in a per-bench
// wrapper outside version control, so a bench without the wrapper silently ran
// capped. The value is the model's own business -- opencode clamps it to the
// model's limit -- so the harness names the configured per-turn ceiling and
// overrides whatever the caller happened to export, exactly once.
func TestTheHarnessSetsTheOutputTokenMaxItself(t *testing.T) {
	t.Parallel()
	const name = "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX"
	for _, environ := range [][]string{
		{name + "=8000"}, // a caller's smaller cap never survives the harness's own
		nil,              // and the name is set when nobody exported it
	} {
		env := nativeChildEnvFrom(environ, "data", "job", "tmp", "", "", "", "", nil)
		var got []string
		for _, kv := range env {
			if strings.HasPrefix(kv, name+"=") {
				got = append(got, kv)
			}
		}
		assert.Equal(t, []string{name + "=128000"}, got, "the child must carry exactly the harness's own ceiling, from %v", environ)
	}
}
