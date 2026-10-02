package update

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envelope is internal/tool's Out as its JSON reads back: the shape every
// skeleton tool's version verb prints.
type envelope struct {
	Result struct {
		Verb   string   `json:"verb"`
		Status string   `json:"status"`
		Exit   int      `json:"exit"`
		Remedy string   `json:"remedy"`
		Why    []string `json:"why"`
	} `json:"result"`
	Facts   map[string]any `json:"facts"`
	Payload string         `json:"payload"`
}

// The banner promises --json on every verb but watch and release, version
// included: one JSON object at exit 0 in internal/tool's envelope, the version
// line its payload, the same answer nova-version's skeleton verb gives.
func TestVersionJSONIsTheSkeletonsEnvelope(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"nova-update", "nova-version"} {
		for _, args := range [][]string{{"version", "--json"}, {"--version", "--json"}} {
			t.Run(name+" "+strings.Join(args, " "), func(t *testing.T) {
				code, out, errs := runTool(t, name, args...)
				require.Equal(t, 0, code, errs)
				assert.Empty(t, errs)
				assert.Equal(t, 1, strings.Count(out, "\n"), "one object: %q", out)
				var e envelope
				require.NoError(t, json.Unmarshal([]byte(out), &e))
				assert.Equal(t, "version", e.Result.Verb)
				assert.Equal(t, "ok", e.Result.Status)
				assert.Equal(t, 0, e.Result.Exit)
				fields, ok := buildinfo.Parse(e.Payload)
				require.True(t, ok, "payload is a version line: %q", e.Payload)
				assert.Equal(t, name, fields.Tool)
				_, line, _ := runTool(t, name, "version")
				assert.Equal(t, strings.TrimSuffix(line, "\n"), e.Payload, "--json carries the line the bare verb prints")
			})
		}
	}
}

// version with an unknown flag or an argument still refuses at exit 2, worded
// as the skeleton words it, in lines or (asked for) as the JSON refusal.
func TestVersionStillRefusesAsTheSkeletonDoes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args   []string
		want   string
		remedy string
	}{
		{[]string{"version", "--bogus"}, "VERSION REFUSED: unknown flag --bogus; the flags of version are --json; run: %s version -h\n", "%s version -h"},
		{[]string{"version", "x"}, `VERSION REFUSED: takes no positional arguments, got "x" (flags come before arguments); run: %s help` + "\n", "%s help"},
	} {
		for _, name := range []string{"nova-update", "nova-version"} {
			t.Run(name+" "+strings.Join(c.args, " "), func(t *testing.T) {
				code, out, errs := runTool(t, name, c.args...)
				assert.Equal(t, 2, code)
				assert.Empty(t, out)
				assert.Equal(t, strings.ReplaceAll(c.want, "%s", name), errs)

				code, out, errs = runTool(t, name, append([]string{"version", "--json"}, c.args[1:]...)...)
				assert.Equal(t, 2, code)
				assert.Empty(t, errs)
				var e envelope
				require.NoError(t, json.Unmarshal([]byte(out), &e))
				assert.Equal(t, "refused", e.Result.Status)
				assert.Equal(t, 2, e.Result.Exit)
				assert.Equal(t, strings.ReplaceAll(c.remedy, "%s", name), e.Result.Remedy)
			})
		}
	}
}
