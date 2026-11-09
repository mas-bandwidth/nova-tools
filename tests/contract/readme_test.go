//go:build functional && !windows

package contract

// The user docs held to the binaries (AGENTS.md: every command, output line and
// refusal quoted in a doc must match what the binary prints). The docs are the root
// README.md, docs/README.md and every cmd/nova-*/README.md, plus the usage lines in
// the header of each play nova-fleet show prints.
//
//   - Every nova command in a shell block or a code span names a verb that
//     "<tool> help <verb>" accepts, and every flag it passes is in that verb's help.
//     A command after "--" (secrets exec, reserve, sandbox) is checked the same way.
//   - A help command (help, -h, --help, version, nova-fleet plays and show) runs
//     cold and exits 0 with its text on stdout.
//   - Every documented refusal is in docRefusals: one that needs nothing but a
//     directory runs cold, and its exit and line must match the doc (<x> and ...
//     match anything); one that needs a store, a fleet or another host is checked
//     for its verb and its remedy command only.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	docTool    = regexp.MustCompile(`^nova-(bus|cairn|config|fleet|reserve|sandbox|secrets)$`)
	docRefusal = regexp.MustCompile(`^(nova-(?:bus|cairn|config|fleet|reserve|sandbox|secrets))((?: [a-z][a-z-]*)*): (.*)$`)
	docReceipt = regexp.MustCompile(`^[A-Z]+ [a-z_]+=`) // nova-reserve: HELD mode=... is a receipt, not a refusal
	shellLangs = map[string]bool{"sh": true, "bash": true, "shell": true, "console": true}
	notAVerb   = regexp.MustCompile(`[<>\[\]{}|*$]|\.\.\.|^[A-Z_]+$`)
)

// A refusalCase is how one documented refusal is reached. argv runs cold in a fresh
// directory ("@tmp" is that directory); vars are the doc's upper-case placeholders; match
// replaces the pattern made from the doc. A case with why runs nothing: why says what it
// needs that a test directory cannot give.
type refusalCase struct {
	tool  string
	argv  []string
	stdin string
	files []string // empty files made in the directory first
	vars  []string
	match string
	goos  string
	why   string
}

const (
	needsStore = "a nova-config store (PostgreSQL)"
	needsBus   = "a nova-bus Redis and a hook"
	needsHeld  = "a machine whose slots another process holds"
	needsSeat  = "a secrets store sealed by sops"
)

// docRefusals is every refusal the docs quote, by its quoted text. A new one in a doc fails
// TestDocs until it is added here, and an entry whose text left the docs fails too.
var docRefusals = map[string]refusalCase{
	`nova-sandbox: bad_read: ...`: {tool: "sandbox", argv: []string{"--read", "@tmp/none", "--", "true"}},
	`nova-cairn append: store "<dir>" does not exist; open it first, or fix --store`: {tool: "cairn",
		argv: []string{"append", "--store", "@tmp/none", "--session", "s", "--entry", "e", "--text", "t"}},
	`nova-bus hook: <what>; reconnecting`:                     {tool: "bus", why: needsBus},
	`nova-bus: <error>; reconnecting`:                         {tool: "bus", why: needsBus},
	"entry \"<id>\" is kept, but its log line is not written": {tool: "cairn", why: "a session log this user cannot write"},
	"stored entry \"<id>\" is corrupt":                        {tool: "cairn", why: "an entry file damaged on disk"},
	`nova-fleet: want a verb or a play; run: nova-fleet help`: {tool: "fleet", argv: []string{}},
	`OPEN FAILED`:   {tool: "cairn", why: "a session opened earlier with another policy"},
	`APPEND FAILED`: {tool: "cairn", why: "an entry appended earlier with other words"},
	`machine add: machine bench-a exists; use: nova-config machine set bench-a`:                                                                                          {tool: "config", why: needsStore},
	`machine remove: machine bench-a still runs services hello; ...`:                                                                                                     {tool: "config", why: needsStore},
	`service add: service x has neither --every nor --keepalive; ...`:                                                                                                    {tool: "config", why: needsStore},
	`service add: service x names secrets (--keys A) and no --seat ...`:                                                                                                  {tool: "config", why: needsStore},
	`service add: service x fetches on a clock (git pull with --loop); fetch on an event (a push) or once per job, never on a clock ...`:                                 {tool: "config", why: needsStore},
	`friend set: friend <friend> has --width 0; ...`:                                                                                                                     {tool: "config", why: needsStore},
	`friend remove: friend <friend> is still named by config.sprint; ...`:                                                                                                {tool: "config", why: needsStore},
	`friend set: friend <friend> was changed or removed by another write since it was read; nothing written; run: nova-config friend show <friend>, then repeat the set`: {tool: "config", why: needsStore},
	`machine list: NOVA_PG_DSN carries a password; ...`:                                                                                                                  {tool: "config", why: needsStore},
	`inventory: takes --list or --host <name>, not both; ...`:                                                                                                            {tool: "config", why: needsStore},
	`inventory: no machine row named nope; ...`:                                                                                                                          {tool: "config", why: needsStore},
	`inventory: service x has corrupt argv (...); run: nova-config service set x --argv <value>`:                                                                         {tool: "config", why: needsStore},
	`friend set: friend <friend>: the store refused the row: ...; run: psql "$NOVA_PG_DSN" -c '\d config.friends'`:                                                       {tool: "config", why: needsStore},
	`no slot within 1s: ...`: {tool: "reserve", why: needsHeld},
	`capacity in use (...)`:  {tool: "reserve", why: needsHeld},
	`this machine's capacity is 8 (...); you asked 3; run: nova-reserve set --slots 3 ...`: {tool: "reserve", why: "a capacity set before, at 8"},
	`NAME: command not found on PATH=...`:                                                  {tool: "reserve", argv: []string{"--dir", "@tmp/r", "--", "NAME"}},
	`NAME: not executable`:                                                                 {tool: "reserve", argv: []string{"--dir", "@tmp/r", "--", "@tmp/NAME"}, files: []string{"NAME"}, vars: []string{"NAME"}},
	`--dir DIR: parent PARENT does not exist; run: mkdir -p PARENT && ...`:                 {tool: "reserve", argv: []string{"--dir", "@tmp/a/b", "--", "true"}, vars: []string{"DIR", "PARENT"}},
	`--dir DIR is a file, not a directory; run: ls -ld DIR`:                                {tool: "reserve", argv: []string{"--dir", "@tmp/DIR", "--", "true"}, files: []string{"DIR"}, vars: []string{"DIR"}},
	`... status could not show this hold; run: sudo rm -f FILE && ...`:                     {tool: "reserve", why: "a holder file another user owns"},
	`nova-reserve: make ended by signal SIGKILL; exit 137`: {tool: "reserve", argv: []string{"--dir", "@tmp/r", "--", "/bin/sh", "-c", "kill -KILL $$"},
		match: `^nova-reserve: /bin/sh ended by signal SIGKILL; exit 137$`},
	`... is not a reservation directory ...`: {tool: "reserve", argv: []string{"status", "--dir", "@tmp/a/b"}},
	`capacity=? # corrupt: ...`:              {tool: "reserve", why: "a capacity file damaged on disk"},
	`nova-sandbox: command killed by signal N (exit 128+N)`: {tool: "sandbox", argv: []string{"--read", "@tmp", "--", "/bin/sh", "-c", "kill -TERM $$"},
		match: `^nova-sandbox: command killed by signal 15 \(exit 143\)$`},
	`nova-sandbox: <reason>: <text>; run: nova-sandbox -h`:                                   {tool: "sandbox", argv: []string{"--read"}},
	`nova-sandbox: bad_value: --read wants an absolute path, got "--"; run: nova-sandbox -h`: {tool: "sandbox", argv: []string{"--read", "--", "true"}},
	`bad_flag`:       {tool: "sandbox", argv: []string{"--no-such-flag", "--", "true"}},
	`bad_value`:      {tool: "sandbox", argv: []string{"--write", "--", "true"}},
	`no_command`:     {tool: "sandbox", argv: []string{"--read", "/usr"}},
	`bad_read`:       {tool: "sandbox", argv: []string{"--read", "relative", "--", "true"}},
	`cwd`:            {tool: "sandbox", why: "a working directory outside the system roots, which the test's own directory may not be"},
	`bad_net`:        {tool: "sandbox", argv: []string{"--net-allow", "127.0.0.1:1", "--", "true"}},
	`bad_gpu`:        {tool: "sandbox", argv: []string{"--gpu", "--", "true"}, goos: "linux"},
	`bad_unix`:       {tool: "sandbox", argv: []string{"--unix", "--", "true"}, goos: "darwin"},
	`bad_env`:        {tool: "sandbox", argv: []string{"--env", "1x", "--", "true"}},
	`not_found`:      {tool: "sandbox", argv: []string{"--", "nosuchcmd-for-the-doc-test"}},
	`not_executable`: {tool: "sandbox", argv: []string{"--read", "@tmp", "--", "@tmp/plain"}, files: []string{"plain"}},
	`no_sandbox`:     {tool: "sandbox", why: "a host without Landlock or sandbox-exec"},
	`usage: seat add <seat> <age1...>; the seat is letters, digits, - and _`:        {tool: "secrets", argv: []string{"seat", "add", "bad/seat", "age1x", "--store", "@tmp"}},
	`set: empty or multi-line value on stdin; the value must be one non-empty line`: {tool: "secrets", why: needsSeat},
	`nova-secrets set: value is over 65536 bytes; a secret is a key or token`:       {tool: "secrets", why: needsSeat},
	`<NAME> is empty (null or "") in <seat>'s files`:                                {tool: "secrets", why: needsSeat},
	`"bad-key" is not an environment variable name, or its value holds a NUL`:       {tool: "secrets", why: needsSeat},
	`git not found on PATH`: {tool: "secrets", why: "a PATH without git, which the store commands look up first"},
	`key file K does not exist`: {tool: "secrets", argv: []string{"exec", "--only", "X", "--store", "@tmp", "--key", "K", "--as", "s", "--", "true"},
		files: []string{"s.yaml"}, vars: []string{"K"}},
}

// A docItem is one thing a doc quotes: a command or a refusal, and where.
type docItem struct {
	loc   string // file:line
	tool  string // the README's own tool, for a refusal quoted without its prefix
	text  string
	words []string // a command's words
	exit  string   // the exit a refusal row documents, when it does
}

// docHarness runs the built tools and caches their help.
type docHarness struct {
	t    *testing.T
	root string
	mu   sync.Mutex
	help map[string]result
	cold int // help commands run
}

func (h *docHarness) run(dir, stdin string, tool string, args ...string) result {
	cmd := exec.Command(filepath.Join(binDir, tool), args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir, "LANG=C"}
	cmd.Stdin = strings.NewReader(stdin)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	code := 0
	if err := cmd.Run(); err != nil {
		code = -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return result{code, so.String(), se.String()}
}

func (h *docHarness) helpOf(tool string, verb ...string) result {
	key := tool + " " + strings.Join(verb, " ")
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.help[key]; ok {
		return r
	}
	r := h.run(h.t.TempDir(), "", tool, append([]string{"help"}, verb...)...)
	h.help[key] = r
	return r
}

func TestDocs(t *testing.T) {
	t.Parallel()
	h := &docHarness{t: t, root: "../..", help: map[string]result{}}
	files, _ := filepath.Glob(filepath.Join(h.root, "cmd", "nova-*", "README.md"))
	files = append([]string{filepath.Join(h.root, "README.md"), filepath.Join(h.root, "docs", "README.md")}, files...)
	var cmds, refusals []docItem
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(h.root, f)
		c, r := readDoc(rel, string(b))
		cmds, refusals = append(cmds, c...), append(refusals, r...)
	}
	cmds = append(cmds, playHeaders(t, h)...)
	if len(cmds) < 100 {
		t.Fatalf("found %d commands in the docs; the reader is broken", len(cmds))
	}
	for _, c := range cmds {
		h.command(c, c.words)
	}
	seen := map[string]bool{}
	for _, r := range refusals {
		seen[r.text] = true
		rc, ok := docRefusals[r.text]
		if !ok {
			t.Errorf("%s: the refusal %q is quoted in a doc and not in docRefusals: add how it is reached (argv), or why it cannot run cold", r.loc, r.text)
			continue
		}
		h.refusal(r, rc)
	}
	ran := 0
	for text, rc := range docRefusals {
		if !seen[text] {
			t.Errorf("docRefusals has %q, which no doc quotes any longer: delete it", text)
		}
		if rc.why == "" && (rc.goos == "" || rc.goos == runtime.GOOS) {
			ran++
		}
	}
	t.Logf("docs: %d commands checked, %d help commands run; %d refusals quoted, %d run cold, the rest checked for verb and remedy", len(cmds), h.cold, len(refusals), ran)
}

// TestFunctionalGuideBuildLines runs every "go build" line in the shell blocks of
// docs/README.md as the guide writes it, once for each tool, with this checkout standing
// where the guide's clone is (nova-tools under $work). A line that names a path the tree
// does not have fails here. GOPROXY=off: the build reads this tree and the module cache,
// never the network. The release workflow runs this test against the tagged tree.
func TestFunctionalGuideBuildLines(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile(filepath.Join(root, "docs", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	found, inBlock, lang := 0, false, ""
	for i, line := range strings.Split(string(guide), "\n") {
		if strings.HasPrefix(line, "```") {
			inBlock, lang = !inBlock, strings.TrimSpace(line[3:])
			continue
		}
		line = strings.TrimSpace(line)
		if !inBlock || !shellLangs[lang] || !strings.HasPrefix(line, "go build ") {
			continue
		}
		found++
		for _, tool := range []string{"bus", "cairn", "config", "fleet", "reserve", "sandbox", "secrets"} {
			t.Run("line"+itoa(i+1)+"/"+tool, func(t *testing.T) {
				t.Parallel()
				work := t.TempDir()
				if err := os.Mkdir(filepath.Join(work, "bin"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(root, filepath.Join(work, "nova-tools")); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("/bin/sh", "-euc", line) // -u: a variable the guide did not set is a failure
				cmd.Dir = work
				cmd.Env = append(os.Environ(), "GOPROXY=off", "work="+work, "tool="+tool, "t="+tool)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("docs/README.md:%d: `%s` with %s for its tool: %v: %s", i+1, line, tool, err, oneline(string(out)))
				}
				if _, err := os.Stat(filepath.Join(work, "bin", "nova-"+tool)); err != nil {
					t.Errorf("docs/README.md:%d: `%s` built no %s: %v", i+1, line, filepath.Join("$work", "bin", "nova-"+tool), err)
				}
			})
		}
	}
	if found == 0 {
		t.Fatal("found no go build line in docs/README.md; the reader is broken")
	}
}

// readDoc returns the nova commands in a doc's shell blocks and code spans, and the
// refusals it quotes: lines and spans shaped "nova-<tool>[ <verb>]: <message>", and the
// first-cell spans of a table under a heading or header about refusals.
func readDoc(file, text string) (cmds, refusals []docItem) {
	tool := ""
	if m := regexp.MustCompile(`^cmd/nova-([a-z]+)/`).FindStringSubmatch(file); m != nil {
		tool = m[1]
	}
	lines := strings.Split(text, "\n")
	inBlock, lang, heading, header := false, "", "", ""
	for i := 0; i < len(lines); i++ {
		line, loc := lines[i], file+":"+itoa(i+1)
		if strings.HasPrefix(line, "```") {
			inBlock, lang = !inBlock, strings.TrimSpace(line[3:])
			continue
		}
		if inBlock {
			if docRefusal.MatchString(line) && !docReceipt.MatchString(docRefusal.FindStringSubmatch(line)[3]) {
				refusals = append(refusals, docItem{loc: loc, tool: tool, text: line})
			}
			if !shellLangs[lang] {
				continue
			}
			for strings.HasSuffix(line, "\\") && i+1 < len(lines) {
				i++
				line = strings.TrimSuffix(line, "\\") + " " + lines[i]
			}
			if lang == "console" {
				line = strings.TrimPrefix(strings.TrimSpace(line), "$ ")
			}
			for _, w := range simpleCommands(line) {
				cmds = append(cmds, docItem{loc: loc, text: strings.Join(w, " "), words: w})
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			heading = line
		}
		if strings.HasPrefix(line, "|") && (i == 0 || !strings.HasPrefix(lines[i-1], "|")) {
			header = line
		}
		spans := regexp.MustCompile("`([^`]+)`").FindAllStringSubmatchIndex(line, -1)
		for k, s := range spans {
			span := line[s[2]:s[3]]
			inRefusalTable := strings.HasPrefix(line, "| `") && k == 0 && line != header &&
				regexp.MustCompile(`(?i)refus`).MatchString(heading+header)
			switch {
			case inRefusalTable && (strings.Contains(span, " ") || regexp.MustCompile(`^[a-z_]+$`).MatchString(span)):
				refusals = append(refusals, docItem{loc: loc, tool: tool, text: span, exit: rowExit(line[s[1]:])})
			case docRefusal.MatchString(span) && !docReceipt.MatchString(docRefusal.FindStringSubmatch(span)[3]):
				refusals = append(refusals, docItem{loc: loc, tool: tool, text: span, exit: rowExit(line[s[1]:])})
			case strings.HasPrefix(span, "nova-") && !regexp.MustCompile(`^\S+ \S+:( |$)|\.\.\.|\. `).MatchString(span):
				// not a refusal's shape ("nova-x <verb>: ..."), an elision, or a printed sentence
				for _, w := range simpleCommands(span) {
					cmds = append(cmds, docItem{loc: loc, text: span, words: w})
				}
			}
		}
	}
	return cmds, refusals
}

// rowExit is the exit a refusal documents right after its quote: "(1)", "(exit 2)", "(2; ...)",
// or a table cell "| 1 |".
func rowExit(rest string) string {
	if m := regexp.MustCompile(`^\s*\((?:exit )?(\d+)[;)]`).FindStringSubmatch(rest); m != nil {
		return m[1]
	}
	if m := regexp.MustCompile(`^\s*\|\s*(\d+)\s*\|`).FindStringSubmatch(rest); m != nil {
		return m[1]
	}
	return ""
}

// playHeaders are the usage lines in the comment header of every play, as nova-fleet show
// prints it: the commands a binary user is told to run. A header line that runs
// ansible-playbook on cmd/nova-fleet/<play>.yml names a file such a user does not have.
func playHeaders(t *testing.T, h *docHarness) []docItem {
	plays := h.run(t.TempDir(), "", "nova-fleet", "plays")
	if plays.code != 0 {
		t.Fatalf("nova-fleet plays: exit %d: %s", plays.code, plays.stderr)
	}
	var out []docItem
	for _, l := range strings.Split(plays.stdout, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 || !regexp.MustCompile(`^[a-z][a-z-]+$`).MatchString(f[0]) {
			continue
		}
		show := h.run(t.TempDir(), "", "nova-fleet", "show", f[0])
		if show.code != 0 {
			t.Errorf("nova-fleet show %s: exit %d: %s", f[0], show.code, show.stderr)
			continue
		}
		lines := strings.Split(show.stdout, "\n")
		for i := 0; i < len(lines) && strings.HasPrefix(lines[i], "#"); i++ {
			line, loc := strings.TrimSpace(strings.TrimPrefix(lines[i], "#")), "nova-fleet show "+f[0]+":"+itoa(i+1)
			for strings.HasSuffix(line, "\\") && i+1 < len(lines) {
				i++
				line = strings.TrimSuffix(line, "\\") + " " + strings.TrimSpace(strings.TrimPrefix(lines[i], "#"))
			}
			if strings.HasPrefix(line, "ansible-playbook ") && strings.Contains(line, "cmd/nova-fleet/") {
				t.Errorf("%s: %q runs a file a binary user does not have; write it as nova-fleet %s [-- ansible-playbook args]", loc, line, f[0])
				continue
			}
			if strings.HasPrefix(line, "nova-") || strings.HasPrefix(line, "env ") {
				for _, w := range simpleCommands(line) {
					out = append(out, docItem{loc: loc, text: line, words: w})
				}
			}
		}
	}
	return out
}

// command checks one documented command: its verb, its flags, a command it wraps after
// "--", and, for help, that it runs.
func (h *docHarness) command(c docItem, words []string) {
	t := h.t
	tool, args := words[0], words[1:]
	sep := slices.Index(args, "--")
	pre := args
	if sep >= 0 {
		pre = args[:sep]
	}
	var pos []string
	for _, w := range pre {
		if strings.HasPrefix(w, "-") || notAVerb.MatchString(w) {
			break
		}
		pos = append(pos, w)
	}
	verb, text := []string(nil), h.helpOf(tool).stdout
	if len(pos) > 0 && pos[0] == "help" {
		if r := h.helpOf(tool, pos[1:]...); r.code != 0 {
			t.Errorf("%s: `%s`: %s %s exits %d: %s", c.loc, c.text, tool, strings.Join(pos, " "), r.code, oneline(r.stderr))
		}
		return
	}
	for k := min(len(pos), 2); k > 0; k-- {
		if r := h.helpOf(tool, pos[:k]...); r.code == 0 {
			verb, text = pos[:k], r.stdout
			break
		}
	}
	if len(pos) > 0 && verb == nil {
		t.Errorf("%s: `%s`: %s has no verb %s (%s help %s refuses it)", c.loc, c.text, tool, pos[0], tool, pos[0])
		return
	}
	if len(verb) == 1 && len(pos) > 1 { // a verb whose help lists sub-verbs takes one of them next
		var subs []string
		for _, m := range regexp.MustCompile(regexp.QuoteMeta(tool+" "+verb[0])+` ([a-z][a-z-]*)`).FindAllStringSubmatch(text, -1) {
			subs = append(subs, m[1])
		}
		if len(subs) > 0 && !slices.Contains(subs, pos[1]) {
			t.Errorf("%s: `%s`: %s %s has no sub-verb %s (its help lists %s)", c.loc, c.text, tool, verb[0], pos[1], strings.Join(slices.Compact(slices.Sorted(slices.Values(subs))), ", "))
			return
		}
	}
	for _, w := range pre[len(verb):] {
		name, _, _ := strings.Cut(w, "=")
		if w == "-h" || w == "--help" || !regexp.MustCompile(`^--?[a-zA-Z][a-zA-Z0-9_-]*$`).MatchString(name) {
			continue
		}
		if !regexp.MustCompile(`(^|[^a-zA-Z0-9_-])` + regexp.QuoteMeta(name) + `([^a-zA-Z0-9_-]|$)`).MatchString(text) {
			t.Errorf("%s: `%s`: %s is not a flag of %s %s (its help does not name it)", c.loc, c.text, name, tool, strings.Join(verb, " "))
		}
	}
	if sep >= 0 {
		inner := args[sep+1:]
		for len(inner) > 0 && (inner[0] == "env" || regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=`).MatchString(inner[0])) {
			inner = inner[1:]
		}
		if len(inner) > 0 && docTool.MatchString(inner[0]) {
			h.command(c, inner)
		}
	}
	cold := slices.Contains(pre, "-h") || slices.Contains(pre, "--help") || slices.Equal(verb, []string{"version"}) ||
		(tool == "nova-fleet" && (slices.Equal(verb, []string{"plays"}) || slices.Equal(verb, []string{"show"})))
	if cold && !slices.ContainsFunc(pre, notAVerb.MatchString) {
		h.cold++
		if r := h.run(t.TempDir(), "", tool, pre...); r.code != 0 || r.stdout == "" || r.stderr != "" {
			t.Errorf("%s: `%s`: want exit 0, text on stdout and nothing on stderr; got exit %d, %d bytes, stderr %q", c.loc, c.text, r.code, len(r.stdout), oneline(r.stderr))
		}
	}
}

// refusal runs one documented refusal cold, or checks the verb and remedy of one that cannot run.
func (h *docHarness) refusal(r docItem, rc refusalCase) {
	t := h.t
	tool := "nova-" + rc.tool
	if r.tool != "" && r.tool != rc.tool {
		t.Errorf("%s: %q is in nova-%s's guide but docRefusals gives it to %s", r.loc, r.text, r.tool, tool)
	}
	msg := r.text
	if m := docRefusal.FindStringSubmatch(r.text); m != nil {
		if verb := strings.Fields(m[2]); len(verb) > 0 {
			if hr := h.helpOf(m[1], verb...); hr.code != 0 {
				t.Errorf("%s: %q: %s has no verb %s", r.loc, r.text, m[1], m[2])
			}
		}
		msg = m[3]
	}
	if m := regexp.MustCompile(`\b(?:run|use): (nova-[a-z]+ [^,;]*)`).FindStringSubmatch(msg); m != nil && !strings.Contains(m[1], "...") {
		if w := simpleCommands(m[1]); len(w) > 0 && docTool.MatchString(w[0][0]) {
			h.command(docItem{loc: r.loc, text: m[1]}, w[0])
		}
	}
	if rc.why != "" || (rc.goos != "" && rc.goos != runtime.GOOS) {
		return
	}
	dir := t.TempDir()
	for _, f := range rc.files {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	argv := make([]string, len(rc.argv))
	for i, a := range rc.argv {
		argv[i] = strings.ReplaceAll(a, "@tmp", dir)
	}
	got := h.run(dir, rc.stdin, tool, argv...)
	pattern := rc.match
	if pattern == "" {
		pattern = regexp.QuoteMeta(r.text)
		pattern = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(pattern, ".+")
		pattern = strings.ReplaceAll(pattern, `\.\.\.`, ".*")
		for _, v := range rc.vars {
			pattern = regexp.MustCompile(`\b`+v+`\b`).ReplaceAllString(pattern, ".+")
		}
	}
	re := regexp.MustCompile(pattern)
	matched := false
	for _, l := range strings.Split(got.stderr, "\n") {
		matched = matched || re.MatchString(l)
	}
	switch {
	case got.code == 0:
		t.Errorf("%s: %q: %s %s exits 0, want the refusal", r.loc, r.text, tool, strings.Join(argv, " "))
	case r.exit != "" && itoa(got.code) != r.exit:
		t.Errorf("%s: %q documents exit %s; %s %s exits %d: %s", r.loc, r.text, r.exit, tool, strings.Join(argv, " "), got.code, oneline(got.stderr))
	case !matched:
		t.Errorf("%s: %q is not what %s %s prints: %q", r.loc, r.text, tool, strings.Join(argv, " "), got.stderr)
	}
}

// simpleCommands splits a shell line into its simple commands, quotes removed, and keeps
// those that run a nova tool (after any VAR=value, env, exec, sudo or time). A redirection
// and its target, a comment, and a command substitution's own words are dropped.
func simpleCommands(line string) [][]string {
	var all [][]string
	var cur []string
	var word strings.Builder
	inWord, quote := false, byte(0)
	flush := func() {
		if inWord {
			cur = append(cur, word.String())
		}
		word.Reset()
		inWord = false
	}
	end := func() {
		flush()
		if len(cur) > 0 {
			all = append(all, cur)
		}
		cur = nil
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case quote != 0 && ch == quote:
			quote = 0
		case quote == '"' && ch == '\\' && i+1 < len(line):
			i++
			word.WriteByte(line[i])
		case quote != 0:
			word.WriteByte(ch)
		case ch == '\'' || ch == '"':
			quote, inWord = ch, true
		case ch == '\\' && i+1 < len(line):
			i++
			word.WriteByte(line[i])
			inWord = true
		case ch == ' ' || ch == '\t':
			flush()
		case ch == '#' && !inWord:
			end()
			return keepNova(all)
		case strings.ContainsRune(";&|()`", rune(ch)) || (ch == '$' && i+1 < len(line) && line[i+1] == '('):
			end()
			if ch == '$' {
				i++
			}
		default:
			word.WriteByte(ch)
			inWord = true
		}
	}
	end()
	return keepNova(all)
}

func keepNova(all [][]string) [][]string {
	var out [][]string
	for _, c := range all {
		var w []string
		for i := 0; i < len(c); i++ {
			if regexp.MustCompile(`^[0-9]*[<>]`).MatchString(c[i]) && !regexp.MustCompile(`^<[A-Za-z]`).MatchString(c[i]) { // <name> is a placeholder
				if regexp.MustCompile(`^[0-9]*[<>]+&?$`).MatchString(c[i]) {
					i++
				}
				continue
			}
			w = append(w, c[i])
		}
		for len(w) > 0 && (slices.Contains([]string{"env", "exec", "sudo", "time", "nohup"}, w[0]) || regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`).MatchString(w[0])) {
			w = w[1:]
		}
		if len(w) > 0 && docTool.MatchString(w[0]) {
			out = append(out, w)
		}
	}
	return out
}

func oneline(s string) string { return trim(strings.ReplaceAll(strings.TrimSpace(s), "\n", " | ")) }

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
