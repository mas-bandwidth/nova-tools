package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func init() {
	register(verb{
		name:    "controls",
		summary: "show the stamp assertion fails on real binaries that lack a stamp, and passes stamped ones",
		help: `usage: go run ./tools/ghrelease controls [--victim <tool>] [--witness <tool>] [--stamp <tag>]

The negative controls of the stamp assertion, run on this tree's real binaries.
Exit 0 every control held; 1 one did not; 2 usage or no scratch space.

A release job that asserts the stamp is worth nothing if the assertion itself
has never been seen to fail, and a tag is the one thing that cannot be quietly
amended, so both outcomes are shown on every certification rather than first on
a tag. The controls build the whole tree (go build ./cmd/...), so a tool added
tomorrow is in them, and run the same assertion the release runs
("ghrelease stamp"):

  1. binaries built with no stamp fail it, by name: every tool answering its vcs
     pseudo-version or devel while the release page names a tag;
  2. the same tree stamped with the release ldflags passes, so the red above is
     the stamp and not the harness;
  3. those binaries renamed the way a release names its artifacts
     (<tool>_<tag>_linux_amd64) pass through the template a release builds;
  4. one required tool (the victim, default nova-tokens) that loses its stamp
     while the others keep theirs fails, by name, and is not demoted to a NOTE,
     and the witness (default nova-bus) is still asserted. Two mutations: the
     "version" variable renamed, and removed. The linker ignores -X in silence
     for a symbol that is missing or renamed, so this is the failure mode the
     assertion exists to catch. Each mutation is asserted to have landed before
     the build, and is applied through a go build overlay, so the working tree
     is never edited.

The refusals that need no binary (the empty stamp, the tag alphabet, the usage
errors, the stale exemption) are unit tests of the verbs.
`,
		do: doControls,
	})
}

func doControls(e env, args []string) int {
	victim, witness, stamp := "nova-tokens", "nova-bus", "v0.0.0-dry-run"
	for i := 0; i < len(args); i++ {
		var dst *string
		switch args[i] {
		case "--victim":
			dst = &victim
		case "--witness":
			dst = &witness
		case "--stamp":
			dst = &stamp
		default:
			fmt.Fprintf(e.stderr, "usage: %s controls [--victim <tool>] [--witness <tool>] [--stamp <tag>]\n", tool)
			return 2
		}
		if i+1 >= len(args) {
			fmt.Fprintf(e.stderr, "usage: %s controls [--victim <tool>] [--witness <tool>] [--stamp <tag>]\n", tool)
			return 2
		}
		i++
		*dst = args[i]
	}

	ldflags, refusal := composeLdflags(stamp)
	if refusal != nil {
		for _, l := range refusal {
			fmt.Fprintln(e.stderr, l)
		}
		return 2
	}
	scratch, err := os.MkdirTemp("", "ghrelease-controls-")
	if err != nil {
		fmt.Fprintf(e.stderr, "%s controls: no scratch directory: %v\n", tool, err)
		return 2
	}
	defer os.RemoveAll(scratch)

	// fail reports a control that did not hold.
	fail := func(format string, a ...any) int {
		fmt.Fprintf(e.stdout, format+"\n", a...)
		return 1
	}
	// assertion runs the release's own assertion over a directory of binaries and
	// returns its output and exit code.
	assertion := func(template string) (string, int) {
		var out bytes.Buffer
		sub := e
		sub.stdout, sub.stderr = &out, &out
		rc := doStamp(sub, []string{stamp, template})
		fmt.Fprint(e.stdout, out.String())
		return out.String(), rc
	}
	build := func(dir string, flags ...string) int {
		args := append([]string{"build"}, flags...)
		args = append(args, "-o", dir+string(os.PathSeparator), "./cmd/...")
		return e.runner().Stream(command{dir: e.dir, name: "go", args: args}, e.stdout, e.stderr)
	}

	// 1. No stamp: the assertion is red, by name.
	unstamped := filepath.Join(scratch, "unstamped")
	if rc := build(unstamped); rc != 0 {
		return fail("the unstamped build failed (go build exit %d); the controls cannot run", rc)
	}
	out, rc := assertion(filepath.Join(unstamped, "%s"))
	if rc == 0 {
		return fail("the assertion PASSED a set of binaries nothing stamped; it would pass a broken release")
	}
	if !strings.Contains(out, "does not report the tag it was built from") {
		return fail("the assertion failed, but not for the missing stamp; that is a different defect")
	}
	fmt.Fprintln(e.stdout, "ok: unstamped binaries are refused by name")

	// 2. The release's stamp: the assertion is green.
	stamped := filepath.Join(scratch, "stamped")
	if rc := build(stamped, "-ldflags", ldflags); rc != 0 {
		return fail("the stamped build failed (go build exit %d); the controls cannot run", rc)
	}
	if _, rc := assertion(filepath.Join(stamped, "%s")); rc != 0 {
		return fail("the assertion REFUSED binaries stamped the way the release stamps them; no release would be possible")
	}
	fmt.Fprintln(e.stdout, "ok: stamped binaries pass")

	// 3. The release's artifact names, through the release's template.
	names, err := toolNames(e.root(), "cmd")
	if err != nil || len(names) == 0 {
		return fail("cmd/ holds no tool; the controls have nothing to name")
	}
	for _, n := range names {
		if err := os.Rename(filepath.Join(stamped, n), filepath.Join(stamped, n+"_"+stamp+"_linux_amd64")); err != nil {
			return fail("%v", err)
		}
	}
	if _, rc := assertion(filepath.Join(stamped, "%s_"+stamp+"_linux_amd64")); rc != 0 {
		return fail("the assertion REFUSED release-named binaries of a legal tag; no release would be possible")
	}
	fmt.Fprintln(e.stdout, "ok: release-named binaries pass through the release's template")

	// 4. One required tool loses its stamp; the rest keep theirs.
	src, err := os.ReadFile(filepath.Join(e.root(), "cmd", victim, "version.go"))
	if err != nil {
		return fail("cmd/%s/version.go cannot be read: %v", victim, err)
	}
	for _, how := range []string{"rename", "remove"} {
		mutated, landed := mutateVersionSource(string(src), how)
		if !landed {
			return fail("the %s fixture did not apply; this control would prove nothing", how)
		}
		abs, err := filepath.Abs(filepath.Join(e.root(), "cmd", victim, "version.go"))
		if err != nil {
			return fail("%v", err)
		}
		mutFile := filepath.Join(scratch, how+"-version.go")
		overlay := filepath.Join(scratch, how+"-overlay.json")
		ov, _ := json.Marshal(map[string]map[string]string{"Replace": {abs: mutFile}})
		if err := os.WriteFile(mutFile, []byte(mutated), 0o644); err != nil {
			return fail("%v", err)
		}
		if err := os.WriteFile(overlay, ov, 0o644); err != nil {
			return fail("%v", err)
		}
		dir := filepath.Join(scratch, "mutated-"+how)
		if rc := build(dir, "-ldflags", ldflags, "-overlay", overlay); rc != 0 {
			return fail("the %s build failed (go build exit %d); the controls cannot run", how, rc)
		}
		out, rc := assertion(filepath.Join(dir, "%s"))
		if rc == 0 {
			return fail("the assertion PASSED a release whose %s answers a pseudo-version (%s)", victim, how)
		}
		if !strings.Contains(out, "FAIL: "+victim+" does not report the tag it was built from") {
			return fail("the assertion failed, but not for %s' missing stamp (%s); that is a different defect", victim, how)
		}
		if strings.Contains(out, "NOTE: "+victim) {
			return fail("%s was demoted to a NOTE (%s); a required tool cannot become exempt by losing a symbol", victim, how)
		}
		if !strings.Contains(out, "ok: "+witness+" "+stamp) {
			return fail("%s stopped reporting the tag while only %s was mutated (%s)", witness, victim, how)
		}
		fmt.Fprintf(e.stdout, "ok: a %s of the stamp is refused by name, with %s still asserted\n", how, witness)
	}
	return 0
}

// mutateVersionSource applies a mutation of the stamp's symbol to a tool's
// version.go: "rename" renames the `version` variable the linker writes,
// "remove" deletes it and passes the empty string where it was read. It reports
// whether the mutation landed, because a mutation that matched nothing would
// rebuild the unmutated tree and prove nothing.
func mutateVersionSource(src, how string) (string, bool) {
	const decl = "var version string"
	const read = "resolveVersion(version, info, ok)"
	lines := strings.Split(src, "\n")
	var out []string
	for _, l := range lines {
		switch {
		case l == decl && how == "rename":
			out = append(out, "var Version string")
		case l == decl && how == "remove":
			continue
		default:
			out = append(out, l)
		}
	}
	res := strings.Join(out, "\n")
	switch how {
	case "rename":
		res = strings.ReplaceAll(res, read, "resolveVersion(Version, info, ok)")
		return res, containsLine(res, "var Version string")
	case "remove":
		res = strings.ReplaceAll(res, read, `resolveVersion("", info, ok)`)
		return res, !containsLine(res, decl) && strings.Contains(src, decl)
	}
	return src, false
}

func containsLine(s, line string) bool {
	return slices.Contains(strings.Split(s, "\n"), line)
}
