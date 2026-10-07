package main

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// widthEnvelope is the family's result envelope as a program reads it: the
// result line, and the facts machine width renders in its two forms
// (docs/STANDARD.md, "When building a tool": one result value, rendered as
// lines or as JSON of the same value; never two shapes).

type widthEnvelope struct {
	Result struct {
		Verb   string `json:"verb"`
		Status string `json:"status"`
		Exit   int    `json:"exit"`
	} `json:"result"`
	Facts struct {
		Machine string `json:"machine"`
		Width   int    `json:"width"`
		Member  bool   `json:"member"`
		Default *bool  `json:"default"`
	} `json:"facts"`
}

// TestMachineWidthUsesResultEnvelope: `machine width --json` answers inside
// the result envelope every other verb uses, carrying the machine, width,
// member and default facts, and the CONFIG WIDTH line renders the same
// value: the positive, explicit zero and unset default widths, the
// store-free --file answer, the missing-machine refusal with its exit, and
// -h that opens no store.
func TestMachineWidthUsesResultEnvelope(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_SPRINT_REDIS"] = "r:1"
	h.machine(t, "m1", "32")
	h.machine(t, "m2", "0")
	h.machine(t, "m3", "")
	for _, c := range []struct {
		name        string
		machine     string
		width       int
		member      bool
		wantDefault bool
		line        string
		envelope    string
	}{
		{
			name: "positive width", machine: "m1", width: 32, member: true,
			line:     "CONFIG WIDTH machine=m1 width=32 member=true\n",
			envelope: `{"result":{"verb":"machine width","status":"ok","exit":0},"facts":{"machine":"m1","width":32,"member":true}}`,
		},
		{
			name: "explicit zero is no member", machine: "m2", width: 0, member: false,
			line:     "CONFIG WIDTH machine=m2 width=0 member=false\n",
			envelope: `{"result":{"verb":"machine width","status":"ok","exit":0},"facts":{"machine":"m2","width":0,"member":false}}`,
		},
		{
			name: "unset width is the default", machine: "m3", width: 0, member: true, wantDefault: true,
			line:     "CONFIG WIDTH machine=m3 width=default member=true\n",
			envelope: `{"result":{"verb":"machine width","status":"ok","exit":0},"facts":{"machine":"m3","width":0,"member":true,"default":true}}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, line, errs := h.run(t, "machine", "width", c.machine, "--pg", dsn)
			require.Equal(t, 0, code, "%q: %d %q %q", c.name, code, line, errs)
			require.Equal(t, "", errs, "%q: %d %q %q", c.name, code, line, errs)
			assert.Equal(t, c.line, line, "%q: the line", c.name)
			code, out, errs := h.run(t, "machine", "width", c.machine, "--pg", dsn, "--json")
			require.Equal(t, 0, code, "%q json: %d %q %q", c.name, code, out, errs)
			require.Equal(t, "", errs, "%q json: %d %q %q", c.name, code, out, errs)
			assert.JSONEq(t, c.envelope, out, "%q: --json is the result envelope, not a bare object", c.name)
			var o widthEnvelope
			require.NoError(t, json.Unmarshal([]byte(out), &o), "%q json", c.name)
			assert.Equal(t, "machine width", o.Result.Verb, "%q: the envelope names the verb", c.name)
			assert.Equal(t, "ok", o.Result.Status, "%q: the status", c.name)
			assert.Equal(t, 0, o.Result.Exit, "%q: the exit", c.name)
			assert.Equal(t, c.machine, o.Facts.Machine, "%q: the machine fact", c.name)
			assert.Equal(t, c.width, o.Facts.Width, "%q: the width fact", c.name)
			assert.Equal(t, c.member, o.Facts.Member, "%q: the member fact", c.name)
			if c.wantDefault {
				require.NotNil(t, o.Facts.Default, "%q: an unset width keeps default=true observable", c.name)
				assert.True(t, *o.Facts.Default, "%q: the default fact", c.name)
			} else {
				assert.Nil(t, o.Facts.Default, "%q: a set width carries no default", c.name)
			}
			width := strconv.Itoa(o.Facts.Width)
			if o.Facts.Default != nil && *o.Facts.Default {
				width = "default"
			}
			assert.Equal(t, "CONFIG WIDTH machine="+o.Facts.Machine+" width="+width+" member="+strconv.FormatBool(o.Facts.Member)+"\n",
				line, "%q: the line renders the envelope's facts", c.name)
		})
	}
	assert.Equal(t, 0, h.redis.opens, "machine width opened Redis")
	// --file answers the same envelope from the local store, with no server.
	fh := newHarness()
	fh.dir = t.TempDir()
	code, _, errs := fh.run(t, "migrate", "--file", "try.json")
	require.Equal(t, 0, code, "migrate --file: %q", errs)
	code, _, errs = fh.run(t, "machine", "add", "mf", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--as", "a1", "--file", "try.json")
	require.Equal(t, 0, code, "machine add --file: %q", errs)
	code, out, errs := fh.run(t, "machine", "width", "mf", "--file", "try.json", "--json")
	require.Equal(t, 0, code, "width --file json: %d %q %q", code, out, errs)
	assert.JSONEq(t, `{"result":{"verb":"machine width","status":"ok","exit":0},"facts":{"machine":"mf","width":4,"member":true}}`, out, "width --file json")
	assert.Equal(t, 0, fh.redis.opens, "width --file opened Redis")
	// A name with no row is refused at exit 1 in both forms: the line to
	// stderr, the --json object to stdout, both with the remedy.
	code, out, errs = h.run(t, "machine", "width", "m9", "--pg", dsn)
	require.Equal(t, 1, code, "no row: %d %q %q", code, out, errs)
	assert.Equal(t, "", out, "no row: stdout")
	assert.Contains(t, errs, "machine m9 not found", "no row: %q", errs)
	assert.Contains(t, errs, "run: nova-config machine list", "no row: %q", errs)
	code, out, errs = h.run(t, "machine", "width", "m9", "--pg", dsn, "--json")
	require.Equal(t, 1, code, "no row json: %d %q %q", code, out, errs)
	assert.Contains(t, out, `"status":"refused"`, "no row json: stdout is the refusal object: %q", out)
	assert.Contains(t, out, "machine m9 not found", "no row json: %q", out)
	assert.Contains(t, out, `"remedy":"nova-config machine list`, "no row json: %q", out)
	// -h answers on stdout at exit 0 and opens neither store.
	hh := newHarness()
	code, out, errs = hh.run(t, "machine", "width", "-h")
	require.Equal(t, 0, code, "-h: %d %q", code, errs)
	assert.Contains(t, out, "machine width", "-h: %q", out)
	assert.Contains(t, out, "--json", "-h names --json")
	assert.Equal(t, "", errs, "-h: %q", errs)
	assert.Equal(t, 0, hh.opens, "-h opened Postgres")
	assert.Equal(t, 0, hh.redis.opens, "-h opened Redis")
}
