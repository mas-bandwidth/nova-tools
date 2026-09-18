package fleet

// A WORKLOAD BODY IS PORTABLE, BECAUSE THE BENCH IS NOT CHOSEN WHEN IT IS WRITTEN.
//
// The embedded workload set was written on and for Linux benches. On 2026-09-18 the M2 Air
// became a darwin/arm64 bench AND a runner host -- `air glenn@100.117.59.68 darwin/arm64
// bench,runner` -- and the first certification run on it found the same class of fault
// #1415 found in the card templates, in three different disguises:
//
//   diag-size       `find -printf '%T@'`  -- BSD find has no -printf, so the oldest-log read
//                   was EMPTY and the card still said OK. The rate is the number that
//                   matters and darwin quietly reported the size as the rate.
//   services-reach  `getent hosts space`  -- there is no getent on macOS, so the empty
//                   answer was read as "the name does not resolve" and the evidence sent a
//                   person to look at /etc/hosts. /etc/hosts on the Air has `space`.
//   runner-path     units only through systemd or `launchctl list` -- a LOADED job and a job
//                   that SURVIVES A REBOOT are two different things on darwin, and the
//                   LaunchAgent plist is the half that was never read.
//
// Two of the three were SILENT: the workload passed and certified a machine on an answer it
// had not actually obtained. That is worse than a failure, and it is the reason this is a
// checker and not a review note.
//
// THE RULE. A workload body is handed to whichever machine the registry names, so it may
// spell only what BOTH operating systems answer -- or it must choose at runtime, on the
// line, out loud. There are two kinds of finding:
//
//   a SPELLING that one OS does not have (`nproc`, `find -printf`, `stat -c`, `sha256sum`).
//   It is refused unless the SAME LINE also names its portable other half, because that IS
//   the portable spelling: `if [ "$(uname -s)" = Darwin ]; then sysctl -n hw.ncpu; else
//   nproc; fi` is one line naming both halves and is never refused.
//
//   a TOOL that lives on one OS only (`getent`, `systemctl`, `launchctl`, `sysctl`). It is
//   refused unless the BODY guards it with `command -v <tool>`, because the guard is what
//   turns "this machine has no getent" into a branch instead of an empty string.
//
// It reads text. It runs nothing, writes nothing, and reaches no machine.
//
// The allowlist is SHRINK-ONLY and matched by (class, spelling) and NEVER by line: a
// line-matched list turns dev red the first time an edit above it shifts a line, which is
// SPEC-CI's shared convention. It is empty today.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// PortabilityFinding is one refused spelling in one workload body: where it is, what was
// written, and what to write instead. The remedy travels WITH the finding, because a
// refusal that does not say what to type is a refusal a person works around.
type PortabilityFinding struct {
	Class    string // the workload's class, which is its file's name
	Source   string // where the workload was read from
	Line     int    // 1-based, within the body
	Spelling string // the rule's name: what was refused
	Text     string // the line, trimmed
	Remedy   string // what to write instead
}

// String renders one finding as the one-line refusal the test prints.
func (f PortabilityFinding) String() string {
	return fmt.Sprintf("%s:%d %s: %s -- %s", f.Class, f.Line, f.Spelling, f.Text, f.Remedy)
}

// spellingRule is a spelling one OS does not have. `portable` are the other halves: when one
// of them is on the SAME LINE, the line has chosen at runtime and is not refused.
type spellingRule struct {
	name     string
	pattern  *regexp.Regexp
	remedy   string
	portable []string
}

// toolRule is a program that exists on one OS only. The guard is `command -v <tool>`
// ANYWHERE in the body: a body that branches on the tool's presence has not assumed it.
type toolRule struct {
	name     string
	tool     *regexp.Regexp
	remedy   string
	guard    string
	portable []string // the other halves, as for a spelling: a line naming one has chosen
}

// The spellings. Each one is a fault somebody has actually had: the first three were found
// on the Air on 2026-09-18, the rest are #1415's table, which came from a schema card that
// died inside a worker on `/usr/bin/time -f`, `nproc`, `go --version` and `java --version`.
var portabilitySpellings = []spellingRule{
	{
		name:     "find -printf",
		pattern:  regexp.MustCompile(`\bfind\b[^\n]*-printf\b`),
		remedy:   "BSD find has no -printf; pick the stat spelling at runtime (`if [ \"$(uname -s)\" = Darwin ]; then stat -f %m; else stat -c %Y; fi`) and -exec it",
		portable: []string{"uname"},
	},
	{
		name:     "getent",
		pattern:  regexp.MustCompile(`\bgetent\b`),
		remedy:   "there is no getent on darwin; `dscacheutil -q host -a name <name>` is the other half, and neither stands alone",
		portable: []string{"dscacheutil", "command -v getent"},
	},
	{
		name:     "stat -c",
		pattern:  regexp.MustCompile(`\bstat\b[^\n]*\s-c(\s|$)`),
		remedy:   "GNU stat is -c and BSD stat is -f; name both on the line, chosen by `uname -s`",
		portable: []string{"-f", "uname"},
	},
	{
		name:     "stat -f",
		pattern:  regexp.MustCompile(`\bstat\b[^\n]*\s-f(\s|$)`),
		remedy:   "BSD stat is -f and GNU stat is -c; name both on the line, chosen by `uname -s`",
		portable: []string{"-c", "uname"},
	},
	{
		name:     "nproc",
		pattern:  regexp.MustCompile(`\bnproc\b`),
		remedy:   "`cores=$(if [ \"$(uname -s)\" = Darwin ]; then sysctl -n hw.ncpu; else nproc; fi)`; neither half stands alone",
		portable: []string{"hw.ncpu", "uname"},
	},
	{
		name:     "/proc",
		pattern:  regexp.MustCompile(`/proc/`),
		remedy:   "darwin has no /proc; ask `sysctl` for the fact, chosen by `uname -s`",
		portable: []string{"uname", "sysctl"},
	},
	{
		name:     "which",
		pattern:  regexp.MustCompile(`(\$\(|` + "`" + `)\s*which\b`),
		remedy:   "`command -v <name>` is the portable presence test; `which` is a program, and not everywhere",
		portable: nil,
	},
	{
		name:     "go --version",
		pattern:  regexp.MustCompile(`\bgo\s+--version\b`),
		remedy:   "the Go toolchain says `go version`, with no dashes, on every platform",
		portable: nil,
	},
	{
		name:     "java --version",
		pattern:  regexp.MustCompile(`\bjava\s+--version\b`),
		remedy:   "`java -version 2>&1`; the long form is not on every JDK and it writes to stderr",
		portable: nil,
	},
	{
		name:     "dotnet version",
		pattern:  regexp.MustCompile(`\bdotnet\s+version\b`),
		remedy:   "`dotnet --version`",
		portable: nil,
	},
	{
		name:     "sha256sum",
		pattern:  regexp.MustCompile(`\b(sha256sum|sha1sum|md5sum)\b`),
		remedy:   "`shasum -a 256` is on both; the GNU spellings are not on darwin",
		portable: []string{"shasum", "uname"},
	},
	{
		name:     "df -B",
		pattern:  regexp.MustCompile(`\bdf\b[^\n]*\s-B`),
		remedy:   "`df -k` and do the arithmetic; BSD df has no -B",
		portable: nil,
	},
	{
		name:     "time -f",
		pattern:  regexp.MustCompile(`(/usr/bin/time|\btime)\s+-f\b`),
		remedy:   "the harness's own timing line; GNU time(1) is not on darwin and the shell builtin has no -f",
		portable: nil,
	},
	{
		name:     "date -d",
		pattern:  regexp.MustCompile(`\bdate\s+-d\b`),
		remedy:   "GNU `date -d` and BSD `date -r`/`date -v` are different programs; name both, chosen by `uname -s`",
		portable: []string{"uname", "date -r", "date -v"},
	},
	{
		name:     "grep -P",
		pattern:  regexp.MustCompile(`\bgrep\b[^\n]*\s-[a-zA-Z]*P`),
		remedy:   "BSD grep has no -P; write the pattern for `grep -E`",
		portable: nil,
	},
	{
		name:     "xargs -r",
		pattern:  regexp.MustCompile(`\bxargs\b[^\n]*\s-[a-zA-Z]*r`),
		remedy:   "BSD xargs has no -r and does not run on empty input anyway; drop it",
		portable: nil,
	},
	{
		name:     "hostname -I",
		pattern:  regexp.MustCompile(`\bhostname\s+-I\b`),
		remedy:   "darwin's hostname has no -I; read the addresses from `ifconfig`/`ip` chosen by `uname -s`",
		portable: []string{"uname"},
	},
	{
		name:     "free",
		pattern:  regexp.MustCompile(`\bfree\s+-[bkmgh]\b`),
		remedy:   "darwin has no free(1); `vm_stat` is the other half, chosen by `uname -s`",
		portable: []string{"vm_stat", "uname"},
	},
	{
		name:     "ldd",
		pattern:  regexp.MustCompile(`\bldd\b`),
		remedy:   "darwin has no ldd; `otool -L` is the other half, chosen by `uname -s`",
		portable: []string{"otool", "uname"},
	},
	{
		name:     "readlink -f",
		pattern:  regexp.MustCompile(`\breadlink\s+-f\b`),
		remedy:   "`readlink -f` is GNU and only recent darwin; write it with a `||` fallback on the same line",
		portable: []string{"||"},
	},
	{
		name:     "sed -i",
		pattern:  regexp.MustCompile(`\bsed\s+(-[a-zA-Z]+\s+)*-i(\s|$)`),
		remedy:   "BSD sed's -i takes a mandatory suffix; write to a temporary file and move it",
		portable: nil,
	},
}

// The one-OS tools. `sysctl` is on both and means different things, so it is here too: a
// body that reads it unguarded has assumed which one it got.
var portabilityTools = []toolRule{
	{
		name:     "systemctl",
		tool:     regexp.MustCompile(`\bsystemctl\b`),
		guard:    "command -v systemctl",
		remedy:   "guard it with `command -v systemctl` and give darwin its launchd half",
		portable: []string{"uname"},
	},
	{
		name:     "launchctl",
		tool:     regexp.MustCompile(`\blaunchctl\b`),
		guard:    "command -v launchctl",
		remedy:   "guard it with `command -v launchctl` and give linux its systemd half",
		portable: []string{"uname"},
	},
	{
		name:     "dscacheutil",
		tool:     regexp.MustCompile(`\bdscacheutil\b`),
		guard:    "command -v dscacheutil",
		remedy:   "guard it with `command -v dscacheutil`; it is darwin's only",
		portable: []string{"uname"},
	},
	{
		name:     "sysctl",
		tool:     regexp.MustCompile(`\bsysctl\b`),
		guard:    "command -v sysctl",
		remedy:   "guard it with `command -v sysctl`; linux's sysctl is a different program with different keys",
		portable: []string{"uname"},
	},
	{
		name:   "apt-get",
		tool:   regexp.MustCompile(`\bapt(-get)?\b`),
		guard:  "command -v apt",
		remedy: "a workload never installs; if it must ask, guard it with `command -v apt`",
	},
	{
		name:     "brew",
		tool:     regexp.MustCompile(`\bbrew\b`),
		guard:    "command -v brew",
		remedy:   "guard it with `command -v brew`; it is darwin's only and not on every darwin",
		portable: []string{"uname"},
	},
}

// PortabilityAllowance is one line of the shrink-only allowlist: a class and a spelling,
// never a line number.
type PortabilityAllowance struct {
	Class    string
	Spelling string
}

// ParsePortabilityAllowlist reads the allowlist: `<class> <spelling>` per line, `#` comments
// and blank lines ignored. A malformed line is a refusal rather than a silent allowance --
// an allowlist nobody can parse is an allowlist that allows everything.
func ParsePortabilityAllowlist(raw string) ([]PortabilityAllowance, error) {
	var out []PortabilityAllowance
	for i, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		class, spelling, ok := strings.Cut(t, " ")
		if !ok || strings.TrimSpace(class) == "" || strings.TrimSpace(spelling) == "" {
			return nil, fmt.Errorf("allowlist line %d: wants `<class> <spelling>`, got %q", i+1, t)
		}
		out = append(out, PortabilityAllowance{Class: strings.TrimSpace(class), Spelling: strings.TrimSpace(spelling)})
	}
	return out, nil
}

// CheckPortability reads one workload's body and answers every refused spelling in it, in
// line order. It is the whole checker: text in, findings out, nothing else touched.
func CheckPortability(w Workload) []PortabilityFinding {
	var out []PortabilityFinding
	body := strings.ReplaceAll(w.Body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	for n, raw := range lines {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		for _, r := range portabilitySpellings {
			if !r.pattern.MatchString(line) {
				continue
			}
			if namesPortableHalf(line, r.portable) {
				continue
			}
			out = append(out, PortabilityFinding{
				Class: w.Class, Source: w.Source, Line: n + 1, Spelling: r.name,
				Text: strings.TrimSpace(raw), Remedy: r.remedy,
			})
		}
	}
	// The tools are body-scoped: the guard may be three lines above the use, and usually is.
	for _, r := range portabilityTools {
		if strings.Contains(body, r.guard) {
			continue
		}
		for n, raw := range lines {
			line := stripComment(raw)
			if !r.tool.MatchString(line) || namesPortableHalf(line, r.portable) {
				continue
			}
			out = append(out, PortabilityFinding{
				Class: w.Class, Source: w.Source, Line: n + 1, Spelling: r.name,
				Text: strings.TrimSpace(raw), Remedy: r.remedy,
			})
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// CheckPortabilityOf runs the checker over a whole set and drops the allowed (class,
// spelling) pairs. It answers the findings and how many allowances were spent, so a caller
// can say `refused=0 allowlisted=0` and a stale allowance can be found and removed.
func CheckPortabilityOf(loads []Workload, allowed []PortabilityAllowance) (findings []PortabilityFinding, spent map[PortabilityAllowance]int) {
	spent = map[PortabilityAllowance]int{}
	allow := map[PortabilityAllowance]bool{}
	for _, a := range allowed {
		allow[a] = true
	}
	for _, w := range loads {
		for _, f := range CheckPortability(w) {
			key := PortabilityAllowance{Class: f.Class, Spelling: f.Spelling}
			if allow[key] {
				spent[key]++
				continue
			}
			findings = append(findings, f)
		}
	}
	return findings, spent
}

// namesPortableHalf says whether this line has already chosen at runtime.
func namesPortableHalf(line string, portable []string) bool {
	for _, p := range portable {
		if strings.Contains(line, p) {
			return true
		}
	}
	return false
}

// stripComment drops a whole-line `#` comment. A comment is where the hurt is EXPLAINED --
// every card in this set names the spellings it refuses in its header -- and a checker that
// read the explanation as the fault would refuse every card that documents itself.
//
// Only a whole-line comment is dropped: a trailing `#` after code cannot be told from a `#`
// inside a string without a shell parser, and refusing on the code half is the safe side.
func stripComment(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return ""
	}
	return line
}
