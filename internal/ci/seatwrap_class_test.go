package ci

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
)

// THE CLASS RULE: NO SCRIPT WRAPS nova-secrets exec AROUND A SEAT TOOL FOR ITS
// REDIS PASSWORD (nova-tools#4052).
//
// Every Redis call the coordinator made went through two bash wrappers in a
// session scratchpad: one ran nova-secrets exec around redis-cli, the other
// around nova-sprint, because nova-sprint on the coordinator seat could not
// read its own password. A new session had to recreate them, and every hand
// fix and table render went through them. nova-sprint, nova-card, nova-swarm
// and nova-wake now take --seat <name> (or NOVA_SEAT) and read the Redis user
// and password from the seat's file through internal/seatcred, the library
// nova-secrets exec runs on; `nova-sprint redis-cli -- <cmd...>` is the hand
// read. A shell line in this tree that still wraps one of them for a Redis
// password teaches the next session to rebuild the wrapper, so the shape is
// refused at the source.

// seatTools are the commands that read a seat's Redis login themselves; a
// wrapper around any of them for a Redis password is the retired shape.
var seatTools = []string{"nova-sprint", "nova-card", "nova-swarm", "nova-wake", "redis-cli"}

var (
	seatWrapExecRe   = regexp.MustCompile(`nova-secrets(?:\.exe)?\s+exec\b`)
	seatWrapOnlyRe   = regexp.MustCompile(`--only[= ]\s*["']?([A-Za-z0-9_,]+)`)
	seatWrapDelimRe  = regexp.MustCompile(`(?:^|\s)--(?:\s|$)`)
	seatWrapToolRe   = regexp.MustCompile(`(?:^|[\s/'"(;=])(` + strings.Join(seatTools, "|") + `)(?:\s|$|['");])`)
	seatWrapRedisKey = regexp.MustCompile(`^(?:NOVA_REDIS_[A-Z0-9_]+|REDISCLI_AUTH)$`)
	seatWrapShebang  = regexp.MustCompile(`\b(ba|z)?sh\b`)
)

// seatWrapLines joins backslash-continued lines, so a wrapper spelled over
// several lines is one command, and returns each with its first line number.
func seatWrapLines(src []byte) (lines []string, starts []int) {
	var cur strings.Builder
	start := 0
	for i, raw := range strings.Split(string(src), "\n") {
		line := strings.TrimRight(raw, "\r")
		if cur.Len() == 0 {
			start = i + 1
		}
		if strings.HasSuffix(line, `\`) {
			cur.WriteString(strings.TrimSuffix(line, `\`))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(line)
		lines, starts = append(lines, cur.String()), append(starts, start)
		cur.Reset()
	}
	if cur.Len() > 0 {
		lines, starts = append(lines, cur.String()), append(starts, start)
	}
	return lines, starts
}

// seatWrapViolations returns "<line>: <tool>" for every command in src that
// runs nova-secrets exec with an --only list of Redis passwords only, around a
// seat tool.
func seatWrapViolations(src []byte) []string {
	var out []string
	lines, starts := seatWrapLines(src)
	for i, line := range lines {
		loc := seatWrapExecRe.FindStringIndex(line)
		if loc == nil {
			continue
		}
		after := line[loc[1]:]
		d := seatWrapDelimRe.FindStringIndex(after)
		if d == nil {
			continue
		}
		flags, target := after[:d[0]], after[d[1]:]
		m := seatWrapOnlyRe.FindStringSubmatch(flags)
		if m == nil {
			continue
		}
		redisOnly := true
		for _, name := range strings.Split(m[1], ",") {
			if name != "" && !seatWrapRedisKey.MatchString(name) {
				redisOnly = false
			}
		}
		if !redisOnly {
			continue
		}
		if t := seatWrapToolRe.FindStringSubmatch(target); t != nil {
			out = append(out, fmt.Sprintf("%d: %s", starts[i], t[1]))
		}
	}
	return out
}

// seatWrapScanned reports whether a repo-relative file is shell this rule
// reads: a .sh/.bash/.zsh file anywhere, an extensionless file whose first
// line is a sh/bash/zsh shebang, or a Markdown file under docs/ (its shell
// examples are what a session copies).
func seatWrapScanned(rel string, head []byte) bool {
	switch path.Ext(rel) {
	case ".sh", ".bash", ".zsh":
		return true
	case ".md":
		return strings.HasPrefix(rel, "docs/")
	case "":
		first, _, _ := bytes.Cut(head, []byte("\n"))
		return bytes.HasPrefix(first, []byte("#!")) && seatWrapShebang.Match(first)
	}
	return false
}

// TestNoSecretsExecWrapsASeatTool sweeps every shell file and docs page in
// the tree and refuses a nova-secrets exec whose --only names Redis passwords
// only, wrapped around nova-sprint, nova-card, nova-swarm, nova-wake or
// redis-cli. The remedy is the tool's own --seat <name> (or NOVA_SEAT), and
// `nova-sprint redis-cli --seat <name> -- <cmd...>` for a hand read.
func TestNoSecretsExecWrapsASeatTool(t *testing.T) {
	t.Parallel()
	tree := repoTree(t)
	scanned := 0
	var violations []string
	for _, f := range tree.Files {
		ext := path.Ext(f.Rel)
		if f.Go || (ext != "" && ext != ".sh" && ext != ".bash" && ext != ".zsh" && ext != ".md") {
			continue
		}
		src := f.Src
		if src == nil {
			b, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatal(err)
			}
			src = b
		}
		if !seatWrapScanned(f.Rel, src) {
			continue
		}
		scanned++
		for _, v := range seatWrapViolations(src) {
			violations = append(violations, f.Rel+":"+v)
		}
	}
	if scanned < 20 {
		t.Fatalf("only %d shell files and docs pages scanned; the tree has more than that, so the filter has stopped matching", scanned)
	}
	if len(violations) > 0 {
		t.Fatalf("%d line(s) wrap nova-secrets exec around a seat tool for its Redis password (#4052); pass the tool --seat <name> (or set NOVA_SEAT) and, for a hand read, run nova-sprint redis-cli --seat <name> -- <cmd...>:\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// TestSeatWrapRuleSeesEachShape is the rule's own control: the two retired
// wrappers and their siblings are red, and a wrapper delivering a key that is
// not a Redis password, a tool outside the set, and the --seat spelling are
// green.
func TestSeatWrapRuleSeesEachShape(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		`exec ~/.local/bin/nova-secrets exec --store $HOME/s --as studio --key k --sops $(command -v sops) --only NOVA_REDIS_COORDINATOR_PASSWORD --require NOVA_REDIS_COORDINATOR_PASSWORD -- /usr/bin/env NOVA_SPRINT_REDIS_USER=coordinator $HOME/.local/bin/nova-sprint "$@" --redis h:6380`,
		`exec nova-secrets exec --as studio --only NOVA_REDIS_COORDINATOR_PASSWORD -- bash -c 'REDISCLI_AUTH=$NOVA_REDIS_COORDINATOR_PASSWORD exec redis-cli -h h -p 6380 "$@"' _ "$@"`,
		"nohup ~/.local/bin/nova-secrets exec \\\n  --as swarm-studio --only NOVA_REDIS_BENCH_PASSWORD -- \\\n  ~/.local/bin/nova-wake beat --as x --store h:6380",
		`nova-secrets exec --as b --only=NOVA_REDIS_BENCH_PASSWORD -- nova-card S/l/1`,
		`nova-secrets exec --as b --only NOVA_REDIS_BENCH_PASSWORD -- timeout 60 nova-swarm native --card c`,
	} {
		if v := seatWrapViolations([]byte("# ok\n" + bad + "\n")); len(v) != 1 || !strings.HasPrefix(v[0], "2: ") {
			t.Fatalf("%q: violations %q, want one at line 2", bad, v)
		}
	}
	good := strings.Join([]string{
		`nova-secrets exec --as b --only OPENROUTER_API_KEY -- nova-swarm native --card c`,
		`nova-secrets exec --as b --only NOVA_REDIS_BENCH_PASSWORD -- nova-tokens ledger --redis h:6380`,
		`nova-sprint table --seat studio --redis h:6380 --once`,
		`nova-sprint redis-cli --seat studio --redis h:6380 -- ZCARD sprint:S:cards`,
		`nova-secrets exec --as b --only GH_TOKEN,NOVA_REDIS_BENCH_PASSWORD -- nova-sprint read post`,
	}, "\n")
	if v := seatWrapViolations([]byte(good)); len(v) != 0 {
		t.Fatalf("good shapes flagged: %q", v)
	}
	for rel, want := range map[string]bool{"scripts/x.sh": true, "docs/A.md": true, "README.md": false, "tools/run": true, "tools/data": false} {
		head := []byte("#!/usr/bin/env bash\n")
		if rel == "tools/data" {
			head = []byte("plain\n")
		}
		if got := seatWrapScanned(rel, head); got != want {
			t.Fatalf("seatWrapScanned(%q) = %v, want %v", rel, got, want)
		}
	}
}
