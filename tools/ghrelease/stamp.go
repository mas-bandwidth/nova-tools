package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// legacyNoVersionVerb names the shipped tools exempt from the stamp assertion,
// one by one, by a person. The list is empty: every shipped tool is required to
// report the tag. It stays a list, and a name can only be added or removed by
// editing this line, so a tool cannot quietly rejoin the exempt set by losing a
// symbol; a name whose tool is gone from the tree is refused as stale.
var legacyNoVersionVerb = []string{}

// shownCap is how much of a binary's version line is printed. What is matched is
// the whole line; what is shown is cut here and marked when it was cut.
const shownCap = 200

func init() {
	register(verb{
		name:    "stamp",
		summary: "assert every shipped binary reports the tag it was built from",
		help: `usage: go run ./tools/ghrelease stamp <expected-tag> <path-template> [cmd-dir]

Runs "<tool> version" for every tool under <cmd-dir> (default cmd) at the path
the template names, and requires each to report <expected-tag> as a whole token
and to name itself. <path-template> holds exactly one %s, replaced by the tool
name: 'dist/%s_v1.2.3_linux_amd64'. Exit 0 every tool reports the tag; 1 a tool
does not (FAIL lines name it); 2 the call cannot be checked (usage, an empty
tag, a bad template, a tag no printed token could equal).

The tool list is discovered from the tree, never written here: the build ships
every cmd/*/, so this walks the same directories, and a tool added tomorrow is
required to report the tag without anyone editing this verb.

What is required is not discovered. The required set is the shipped set, and
the only tools not in it are named in the exemption list, legacyNoVersionVerb,
one by one, by a person; the list is empty. Nothing about a tool's own source
decides whether it is required: a renamed or deleted "version" symbol is the
failure this check exists to catch, so it cannot also be what excuses the tool.
The linker ignores -X main.version in silence when the symbol is missing or
renamed, which is why the binaries are run.

The tag is matched as a whole token with = counted as a separator, so v0.1 does
not pass for v0.11 and a tag inside a path does not count. The printed line is
shown cut at 200 characters and matched whole.
`,
		do: doStamp,
	})
}

func doStamp(e env, args []string) int {
	if len(args) < 2 || len(args) > 3 {
		fmt.Fprintf(e.stderr, "usage: %s stamp <expected-tag> <path-template> [cmd-dir]\n", tool)
		io.WriteString(e.stderr, "  path-template holds one %s, replaced by the tool name: 'dist/%s_v1.2.3_linux_amd64'\n")
		fmt.Fprintln(e.stderr, "  cmd-dir defaults to cmd")
		return 2
	}
	tag, template := args[0], args[1]
	cmdDir := "cmd"
	if len(args) == 3 {
		cmdDir = args[2]
	}

	if tag == "" {
		fmt.Fprintln(e.stderr, "refusing: no expected tag given; this check would assert nothing and pass")
		return 2
	}
	if !strings.Contains(template, "%s") {
		fmt.Fprintf(e.stderr, "refusing: the path template <%s> holds no %%s, so every tool would resolve to one path\n", template)
		return 2
	}
	// Exactly one %s and no other %: the tag can travel inside the template, and
	// a % that is not the one %s is a directive that is not there.
	before, after, _ := strings.Cut(template, "%s")
	if strings.Contains(before+after, "%") {
		fmt.Fprintf(e.stderr, "refusing: the path template <%s> holds a %% beyond its single %%s\n", template)
		fmt.Fprintln(e.stderr, "  a stray % reads as a directive that is not there")
		fmt.Fprintln(e.stderr, "  (a tag carrying % is refused by ghrelease ldflags before anything is built)")
		return 2
	}
	// A tag this check could never match is a check that would only ever fail.
	// The match is whole-token with = counted as a separator, so a tag holding =
	// or white space can never be that token however the binary prints it.
	if hasSpace(tag) {
		fmt.Fprintf(e.stderr, "refusing: the expected tag <%s> carries whitespace; no printed token can equal it\n", tag)
		return 2
	}
	if strings.Contains(tag, "=") {
		fmt.Fprintf(e.stderr, "refusing: the expected tag <%s> contains =, which this check reads as a token separator\n", tag)
		fmt.Fprintln(e.stderr, "  (ghrelease ldflags refuses such a tag before the build)")
		return 2
	}

	legacy := e.legacy
	if legacy == nil {
		legacy = legacyNoVersionVerb
	}
	// A stale exemption is a defect, checked before anything is run: a name with
	// no directory is a tool that was renamed or a list pointed at another tree,
	// and either way the list has stopped saying what it claims to say.
	for _, name := range legacy {
		if fi, err := os.Stat(filepath.Join(e.root(), cmdDir, name)); err != nil || !fi.IsDir() {
			fmt.Fprintf(e.stdout, "FAIL: %s is exempted from the stamp assertion but %s/%s does not exist\n", name, cmdDir, name)
			fmt.Fprintln(e.stdout, "  the exemption list is a debt, not a place names are left behind")
			fmt.Fprintln(e.stdout, "  take the name off legacyNoVersionVerb, or point this check at the shipped tree")
			return 1
		}
	}

	tools, _ := toolNames(e.root(), cmdDir)
	if len(tools) == 0 {
		fmt.Fprintf(e.stdout, "FAIL: %s/ matched no tool directories; this check asserted nothing and would pass\n", cmdDir)
		return 1
	}

	var notes strings.Builder
	note := func(format string, a ...any) { fmt.Fprintf(&notes, "NOTE: "+format+"\n", a...) }
	asserted, exempted, seen := 0, 0, 0
	for _, name := range tools {
		seen++
		bin := strings.Replace(template, "%s", name, 1)
		binPath := bin
		if !filepath.IsAbs(binPath) {
			binPath = filepath.Join(e.root(), binPath)
		}
		if fi, err := os.Stat(binPath); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			fmt.Fprintf(e.stdout, "FAIL: %s is built by the release loop but there is no runnable binary at %s\n", name, bin)
			return 1
		}

		full, rc := e.runner().Output(command{dir: e.dir, name: binPath, args: []string{"version"}})
		// One line, matched whole, shown bounded. A refusal can be a whole usage
		// message, and a check that pastes eleven of those is a check nobody
		// reads to the end; what is matched is the full output, because a tool
		// whose version line carries the tag past the cut would otherwise fail
		// for a defect here and not for anything wrong with the binary.
		full = strings.ReplaceAll(chomp(full), "\n", " ")
		shown := full
		if r := []rune(full); len(r) > shownCap {
			shown = string(r[:shownCap]) + "..."
		}

		if contains(legacy, name) {
			// Named and printed rather than skipped in silence, so the day an
			// exempt tool starts answering the tag is visible on the release log.
			if rc != 0 {
				note("%s has no version print today: `%s version` exited %d: %s", name, name, rc, shown)
			} else {
				note("%s prints an identity that is not the release stamp: %s", name, shown)
			}
			note("  it is exempt by name until the common version verb lands: #121")
			exempted++
			continue
		}

		if rc != 0 {
			fmt.Fprintf(e.stdout, "FAIL: %s is a shipped binary and must report the tag, but `%s version` exited %d\n", name, name, rc)
			fmt.Fprintf(e.stdout, "  it printed: %s\n", shown)
			fmt.Fprintln(e.stdout, "  a stamp no verb can read is a stamp nobody can check; wire version into main.go's dispatch")
			fmt.Fprintln(e.stdout, "  (the only tools not held to the tag are the legacy ones, named in legacyNoVersionVerb)")
			return 1
		}

		// The tag as a whole token, so v0.1 does not pass for v0.11. = is a
		// separator too, so no tag is ever matched out of the value half of a
		// key=value extra: every binary puts the stamp in field two, alone.
		if !strings.Contains(" "+strings.ReplaceAll(full, "=", " ")+" ", " "+tag+" ") {
			fmt.Fprintf(e.stdout, "FAIL: %s does not report the tag it was built from\n", name)
			fmt.Fprintf(e.stdout, "  want the token: %s\n", tag)
			fmt.Fprintf(e.stdout, "  `%s version` printed: %s\n", name, shown)
			fmt.Fprintln(e.stdout, "  the linker ignores -X main.version in silence when the symbol is missing or renamed")
			return 1
		}

		// The print names itself: two binaries answering with the same tag and no
		// name is one copy-paste away from a version verb that reports another
		// tool's identity.
		if !strings.Contains(full, name) {
			fmt.Fprintf(e.stdout, "FAIL: %s version does not name the tool it is reporting for\n", name)
			fmt.Fprintf(e.stdout, "  `%s version` printed: %s\n", name, shown)
			return 1
		}

		fmt.Fprintf(e.stdout, "ok: %s\n", shown)
		asserted++
	}

	// Every shipped tool exempt is a run that asserted nothing.
	if asserted == 0 {
		fmt.Fprintf(e.stdout, "FAIL: every one of the %d shipped tools is on the exemption list, so this check\n", seen)
		fmt.Fprintf(e.stdout, "  asserted no stamp at all; either the list has outgrown the tree or %s is not\n", cmdDir)
		fmt.Fprintln(e.stdout, "  this repository's tree")
		return 1
	}

	fmt.Fprint(e.stdout, notes.String())
	fmt.Fprintf(e.stdout, "asserted the %s stamp on %d of %d shipped tools (%d exempt until #121)\n", tag, asserted, seen, exempted)
	return 0
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
