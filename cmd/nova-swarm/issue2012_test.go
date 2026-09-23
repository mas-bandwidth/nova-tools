package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// nova-tools #2012, in its own title: "launchers: macOS bash 3.2 has no `mapfile`; zsh
// does not word-split unquoted variables: every fleet script must run under `/bin/bash`
// 3.2 and be linted for it".
//
// Two self-inflicted outages in one night, same class. (1) A launcher used `mapfile`;
// launchers execute on the coordinator -- a Mac whose /bin/bash is 3.2 and has no
// `mapfile` at any spelling -- so EVERY Linux launch failed for ~10 minutes on
// `mapfile: command not found` while the darwin benches kept working. (2) Earlier, a
// zsh one-liner passed `$C` unquoted expecting word splitting; four loops printed usage
// and died. The lanes also measured zsh eating `:r` in refspecs (`$NEW:refs/...`) and
// `set -- $var` not splitting.
//
// So the fleet's own tool lint is the check the issue asks for: `nova-swarm lint --fleet
// <script>` reads the one launcher script it is handed -- no model, no probe, one file,
// before any launch -- and fails it on the three shapes that night cost: a first line
// that does not name bash, a bash-4-only builtin macOS's 3.2 has not got, and an
// unquoted parameter expansion relying on a word split zsh never makes. This test is red
// against the tree before that lint exists: `--fleet` is an unknown flag at exit 2 that
// prints nothing on stdout.

// fleetScript writes one launcher script under t.TempDir() and returns its path.
func fleetScript(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	write(t, path, body)
	return path
}

// The first outage's launcher, verbatim in shape: a bash shebang, one `mapfile`, and a
// loop over the array it filled. Everything except the `mapfile` line is spelled the
// way the lint wants it, so the one defect the night cost is the one the lint names.
const issue2012Mapfile = `#!/bin/bash
mapfile -t cards < "$queue"
for c in "${cards[@]}"; do
	nova-swarm batch --id "$c" --deadline 1800
done
`

// The second outage's zsh one-liner, and the two shapes the lanes saw with it: `$C`
// passed unquoted where the split was the point, `set -- $var` which zsh does not
// split, and `$NEW:refs/...` whose `:r` zsh eats as a modifier. Every quoted
// expansion on these lines is deliberate: the unquoted ones are the defect.
const issue2012ZshSplit = `#!/bin/bash
for c in $CARDS; do
	echo "$c"
done
set -- $ARGS
git push -q origin $NEW:refs/heads/rowan/integration
`

func TestIssue2012(t *testing.T) {
	t.Run("mapfile-is-not-a-bash-32-builtin", func(t *testing.T) {
		path := fleetScript(t, "mapfile-launcher.sh", issue2012Mapfile)
		exit, stdout, stderr := runSwarm(t, "lint", "--fleet", path)
		if exit != 2 {
			t.Fatalf("a launcher that reads lines with `mapfile` drifts at exit 2 -- the coordinator's /bin/bash is 3.2 and has no `mapfile`, which is the first outage of #2012 -- got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
		}
		if !strings.Contains(stdout, "LINT DRIFT script=mapfile-launcher.sh bash4-builtin: 2:") {
			t.Fatalf("the drift names the rule, the script and the `mapfile` line:\n%s", stdout)
		}
		if !strings.Contains(stdout, "mapfile") {
			t.Fatalf("the drift's excerpt quotes the `mapfile` line so the writer reads which word it is:\n%s", stdout)
		}
		if !strings.Contains(stdout, " remedy=") {
			t.Fatalf("every drift carries its remedy, which is the whole of the card-writer lesson #1464 recorded:\n%s", stdout)
		}
		if strings.Contains(stdout, "unquoted-expansion") {
			t.Fatalf("every other expansion on the launcher is quoted; `mapfile` is its one defect:\n%s", stdout)
		}
	})

	t.Run("zsh-does-not-word-split-unquoted-variables", func(t *testing.T) {
		path := fleetScript(t, "zsh-one-liner.sh", issue2012ZshSplit)
		exit, stdout, stderr := runSwarm(t, "lint", "--fleet", path)
		if exit != 2 {
			t.Fatalf("a launcher that leans on unquoted word splitting drifts at exit 2 -- zsh does not split it and four loops died on exactly this, which is the second outage of #2012 -- got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
		}
		for _, want := range []string{
			"unquoted-expansion: 2:", // for c in $CARDS
			"unquoted-expansion: 5:", // set -- $ARGS
			"unquoted-expansion: 6:", // $NEW:refs/... whose `:r` zsh eats
		} {
			if !strings.Contains(stdout, "LINT DRIFT script=zsh-one-liner.sh "+want) {
				t.Errorf("the drift %s is the zsh shape #2012 measured and is not named:\n%s", want, stdout)
			}
		}
		for _, no := range []string{"bash4-builtin", "bash-shebang"} {
			if strings.Contains(stdout, no) {
				t.Errorf("`%s` is not this script's defect: the shebang names bash and no bash-4 builtin is used:\n%s", no, stdout)
			}
		}
		if !strings.Contains(stdout, " remedy=") {
			t.Fatalf("every drift carries its remedy:\n%s", stdout)
		}
	})

	t.Run("the-fleet-runs-under-bin-bash", func(t *testing.T) {
		// A zsh shebang on an otherwise clean launcher: the interpreter line is the
		// whole defect. A launcher missing its `#!` line is the same drift -- run by
		// name on the coordinator it is whatever shell typed it, and there that is zsh.
		cases := []struct {
			name string
			body string
		}{
			{"zsh-shebang", "#!/bin/zsh\nfor c in \"$CARDS\"; do\n\techo \"$c\"\ndone\n"},
			{"no-shebang", "for c in \"$CARDS\"; do\n\techo \"$c\"\ndone\n"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				path := fleetScript(t, tc.name+".sh", tc.body)
				exit, stdout, stderr := runSwarm(t, "lint", "--fleet", path)
				if exit != 2 {
					t.Fatalf("a fleet script the coordinator cannot run under /bin/bash drifts at exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
				}
				if !strings.Contains(stdout, "LINT DRIFT script="+tc.name+".sh bash-shebang: 1:") {
					t.Fatalf("the drift names the interpreter line, which is line 1:\n%s", stdout)
				}
				if strings.Contains(stdout, "unquoted-expansion") {
					t.Fatalf("every expansion on this launcher is quoted; the interpreter line is its one defect:\n%s", stdout)
				}
			})
		}
	})

	t.Run("a-32-way-launcher-passes", func(t *testing.T) {
		// The launcher rewritten the way the issue wants it: bash named on line 1,
		// lines read with `while IFS= read -r` (the 3.2 way, `mapfile` named only in a
		// comment, which the lint must not read as code), and every expansion in
		// double quotes.
		clean := strings.Join([]string{
			"#!/usr/bin/env bash",
			"# read the queue the 3.2 way: no mapfile, every expansion quoted",
			"while IFS= read -r c; do",
			"	[ -n \"$c\" ] || continue",
			"	nova-swarm batch --id \"$c\" --deadline 1800",
			"done < \"$queue\"",
			"",
		}, "\n")
		path := fleetScript(t, "clean-launcher.sh", clean)
		exit, stdout, stderr := runSwarm(t, "lint", "--fleet", path)
		if exit != 0 {
			t.Fatalf("a launcher that runs under /bin/bash 3.2 with every expansion quoted lints clean at exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
		}
		if !strings.Contains(stdout, "LINT OK script=clean-launcher.sh checks=3") {
			t.Fatalf("the OK line names the script and how many checks ran:\n%s", stdout)
		}
		if lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n"); len(lines) != 1 {
			t.Fatalf("LINT OK is one line, got %d:\n%s", len(lines), stdout)
		}
		if stderr != "" {
			t.Fatalf("a clean lint writes nothing to stderr: %q", stderr)
		}
	})

	t.Run("mapfile-inside-quotes-is-prose", func(t *testing.T) {
		// Held on #2872 (emma, stella): fleetLineScan kept quoted bytes in bare, so a
		// quoted `mapfile` drifted bash4-builtin. Quoted text is prose; `$( )` is code.
		body := strings.Join([]string{
			"#!/usr/bin/env bash",
			"echo \"mapfile\"",
			"echo 'readarray is bash 4'",
			"printf '%s\\n' \"use while read, not mapfile\"",
			"",
		}, "\n")
		path := fleetScript(t, "quoted-mapfile.sh", body)
		exit, stdout, stderr := runSwarm(t, "lint", "--fleet", path)
		if exit != 0 || !strings.Contains(stdout, "LINT OK script=quoted-mapfile.sh checks=3") {
			t.Fatalf("`mapfile` inside single or double quotes is prose and lints clean at exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
		}
		code := fleetScript(t, "subst-mapfile.sh", "#!/usr/bin/env bash\nx=\"$(mapfile -t a < f)\"\n")
		exit, stdout, _ = runSwarm(t, "lint", "--fleet", code)
		if exit != 2 || !strings.Contains(stdout, "bash4-builtin: 2:") {
			t.Fatalf("`mapfile` inside a quoted `$( )` is still code and drifts, got %d\n%s", exit, stdout)
		}
	})

	t.Run("card-and-fleet-are-two-inputs", func(t *testing.T) {
		path := fleetScript(t, "two-inputs.sh", "#!/bin/bash\nexit 0\n")
		exit, _, stderr := runSwarm(t, "lint", "--fleet", path, "--card", path)
		if exit != 2 {
			t.Fatalf("--card and --fleet name two different inputs and one run must say so at exit 2, got %d", exit)
		}
		if !strings.Contains(stderr, "--card and --fleet") {
			t.Fatalf("the refusal names the two flags it was handed:\n%s", stderr)
		}
		exit, _, stderr = runSwarm(t, "lint", "--fleet", path, "--typed")
		if exit != 2 {
			t.Fatalf("the card checks over a shell script are a guess about a different file and must be refused at exit 2, got %d", exit)
		}
		if !strings.Contains(stderr, "--typed") {
			t.Fatalf("the refusal names the card flag it was handed:\n%s", stderr)
		}
	})
}
