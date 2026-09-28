package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
)

// Stella's hold at 62b09a24: the lineup run marker reaches each record.
// `lineup publish` stamps $NOVA_LINEUP_RUN (what bench-conform inherits from
// `nova-sprint lineup`) as the record's run field, and --run overrides it.
func TestLineupPublishStampsRunMarker(t *testing.T) {
	mr := miniredis.RunT(t)
	yml := filepath.Join(t.TempDir(), "all.yml")
	if err := os.WriteFile(yml, []byte("nova_build: \"v0.16.0-dev.e4386caa\"\nsecrets_rev: \"0fa4aa554bef\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := strings.Join([]string{
		preflight.KeyBuild + "=e4386caa",
		preflight.KeySecretsStore + "=branch=main,upstream=yes,rev=0fa4aa554bef,clean=yes",
		preflight.KeyCloneVerb + "=verb=ok,mirrors=nova-tools:nova-work:rowan-tools:schema",
		preflight.KeyPushCredential + "=present",
		preflight.KeyMirrorAge + "=42",
		preflight.KeyResultsRoot + "=~/nova-bench/results",
		preflight.KeyFinishedJobdirs + "=0",
	}, "\n") + "\n"
	publish := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		argv := append([]string{"--redis", mr.Addr(), "--bench", "hulk", "--all-yml", yml}, args...)
		if code := cmdLineupPublish(context.Background(), argv, strings.NewReader(probe), &out, &errb); code != 0 {
			t.Fatalf("publish exit %d: %s%s", code, out.String(), errb.String())
		}
		return mr.HGet("bench:hulk:conform", "run")
	}
	t.Setenv(preflight.RunMarkerEnv, "run-from-env")
	if got := publish(); got != "run-from-env" {
		t.Fatalf("run field %q, want the env marker", got)
	}
	if got := publish("--run", "run-from-flag"); got != "run-from-flag" {
		t.Fatalf("run field %q, want the flag marker", got)
	}
}
