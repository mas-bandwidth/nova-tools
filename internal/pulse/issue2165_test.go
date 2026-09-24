package pulse

// nova-tools#2165: cut --pool wrote <id>.md while fill only reads card-<n>.md.
// The documented first run cuts pool.tsv to --out and fills --once over that
// --out as --ready. On the bug the out directory holds first-fix.md and
// first-read.md, fill launches nothing at exit 0, and the FILL REFUSED remedy
// names `nova-pulse cut` -- the verb that just produced the files. A pool cut
// must write card-<id>.md, the one filename contract fill globs (cardFileName),
// so everything cut produced launches with no stray refusal.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue2165(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "templates")
	writeTemplates(t, tmpl, map[string]string{"read": readTemplate, "fix": fixTemplate}, defaultBenches)
	poolPath := filepath.Join(dir, "pool.tsv")
	pool := "mas-bandwidth/nova-tools\tfirst-read\tread\tRead the onboarding\tread\n" +
		"mas-bandwidth/nova-tools\tfirst-fix\tfix\tFix the flag typo\tfix\n"
	if err := os.WriteFile(poolPath, []byte(pool), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "cards")
	var stdout, stderr strings.Builder
	if code := Cut(CutInput{
		Pool: poolPath, Templates: tmpl, Out: out, Root: filepath.Join(dir, "root"), Max: 20,
		Stdout: &stdout, Stderr: &stderr,
	}); code != 0 {
		t.Fatalf("cut --pool exit = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	// Every card file cut writes carries the card- prefix fill globs.
	for _, want := range []string{"card-first-read.md", "card-first-fix.md"} {
		if _, err := os.Stat(filepath.Join(out, want)); err != nil {
			entries, _ := os.ReadDir(out)
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("cut --pool wrote no %s (out holds %q): pool cards must be card-<id>.md, the names fill reads", want, names)
		}
	}
	if strays := strayCards(out); len(strays) > 0 {
		var names []string
		for _, s := range strays {
			names = append(names, filepath.Base(s))
		}
		t.Fatalf("cut --pool left %q in --out that fill would refuse as strays", names)
	}

	// The documented second half: fill --once over that --out as --ready.
	launched := filepath.Join(dir, "launched")
	l := &laneLauncher{}
	var fout, ferr bytes.Buffer
	fcode := Fill(FillInput{
		Ready: out, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stdout:   &fout,
		Stderr:   &ferr,
		Capacity: laneCap{"bench-a": 10},
		Launcher: l,
	})
	if fcode != 0 {
		t.Fatalf("fill --once exit = %d, want 0; stdout=%q stderr=%q", fcode, fout.String(), ferr.String())
	}
	if len(l.calls) != 2 {
		t.Fatalf("fill launched %d cards, want 2 (every pool card); stdout=%q stderr=%q", len(l.calls), fout.String(), ferr.String())
	}
	if strings.Contains(ferr.String(), "FILL REFUSED") {
		t.Fatalf("fill refused a directory cut just produced: stderr=%q", ferr.String())
	}
}
