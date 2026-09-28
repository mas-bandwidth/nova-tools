// A fake gh, for the tests of nova-wake's forge sources. nova-wake's view of a
// forge is gh's, and a test that talked to a forge would be a test with a
// network in it.
//
// One directory, named in NOVA_WAKE_FAKE_GH. Every invocation is appended to
// `calls`, one line, and the answer is read from a file named for what was
// asked:
//
//	<number>.json           `gh pr view <number> --json ...`      (the entry source)
//	user.json               `gh api user`                         (--owned-prs)
//	prlist-<o>-<r>.json     `gh pr list --repo <o>/<r> ...`       (--owned-prs)
//	pr-<o>-<r>-<n>.json     `gh api graphql` standing read        (--pr)
//	pr-<o>-<r>-<n>.nodes.json   the same with -F fetch=1          (--not-mine)
//	runs-<sha>.json         `gh api .../commits/<sha>/check-runs` (--run)
//	status-<sha>.json       `gh api .../commits/<sha>/status`     (--run)
//	ref-<o>-<r>-<name>.json `gh api .../git/ref/heads/<name>`     (--ref)
//
// Beside any of them, `<key>.exit` is an exit code and `<key>.stderr` what it
// prints, so unreadable: can be proved without a forge that misbehaves.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	dir := os.Getenv("NOVA_WAKE_FAKE_GH")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "the fake gh needs NOVA_WAKE_FAKE_GH")
		os.Exit(2)
	}
	args := os.Args[1:]
	appendLine(filepath.Join(dir, "calls"), strings.Join(args, " "))
	key, fallback := keyOf(args)
	if code := strings.TrimSpace(read(filepath.Join(dir, key+".exit"))); code != "" {
		fmt.Fprintln(os.Stderr, read(filepath.Join(dir, key+".stderr")))
		c, _ := strconv.Atoi(code)
		os.Exit(c)
	}
	out := read(filepath.Join(dir, key+".json"))
	if out == "" {
		out = fallback
	}
	if out == "" {
		fmt.Fprintf(os.Stderr, "the fake gh has no answer for %s\n", key)
		os.Exit(1)
	}
	fmt.Print(out)
}

// keyOf names the fixture file this invocation is answered from, and the answer
// to give when the test wrote none.
func keyOf(args []string) (key, fallback string) {
	field := func(name string) string {
		for i, a := range args {
			if (a == "-F" || a == "-f") && i+1 < len(args) {
				if v, ok := strings.CutPrefix(args[i+1], name+"="); ok {
					return v
				}
			}
		}
		return ""
	}
	has := func(want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}
	switch {
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		number := ""
		for i, a := range args {
			if a == "view" && i+1 < len(args) {
				number = args[i+1]
			}
		}
		return number, `{"state":"OPEN","statusCheckRollup":[]}`
	case len(args) >= 2 && args[0] == "pr" && args[1] == "list":
		repo := ""
		for i, a := range args {
			if a == "--repo" && i+1 < len(args) {
				repo = args[i+1]
			}
		}
		return "prlist-" + strings.ReplaceAll(repo, "/", "-"), "[]"
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql":
		key = fmt.Sprintf("pr-%s-%s-%s", field("owner"), field("repo"), field("number"))
		if has("fetch=1") {
			key += ".nodes"
		}
		return key, ""
	case len(args) >= 2 && args[0] == "api" && args[1] == "user":
		return "user", `{"login":"me"}`
	case len(args) >= 2 && args[0] == "api":
		target := args[1]
		if i := strings.IndexByte(target, '?'); i >= 0 {
			target = target[:i]
		}
		parts := strings.Split(target, "/")
		switch {
		case strings.HasSuffix(target, "/check-runs") && len(parts) >= 5:
			return "runs-" + parts[len(parts)-2], `{"total_count":0,"check_runs":[]}`
		case strings.HasSuffix(target, "/status") && len(parts) >= 5:
			return "status-" + parts[len(parts)-2], `{"statuses":[]}`
		case strings.Contains(target, "/git/ref/heads/") && len(parts) >= 3:
			owner, repo := parts[1], parts[2]
			name := target[strings.Index(target, "/git/ref/heads/")+len("/git/ref/heads/"):]
			return fmt.Sprintf("ref-%s-%s-%s", owner, repo, strings.ReplaceAll(name, "/", "-")), ""
		}
		return strings.ReplaceAll(target, "/", "-"), ""
	}
	return strings.Join(args, "-"), ""
}

func read(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}
