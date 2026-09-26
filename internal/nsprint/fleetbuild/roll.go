package fleetbuild

// The pieces of the roll (#4332) that fleet release (#4356 A) runs after the
// build: dev's tip, the bench play through ansible, the beat restart through
// ansible, and the verify from the beats. None of them reaches a host by
// ssh: the play and the restart go through the play directory's inventory
// (fleet-changes-only-through-ansible), the verify reads the store.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

const (
	// DefaultPlay is the play that installs the declared nova build on
	// every bench (the tools play of the rowan-tools fleet directory, its
	// tools role).
	DefaultPlay = "tools.yml"
	// PlayDirEnv names the fleet play directory; unset, it is
	// <home>/DefaultPlayDirRel.
	PlayDirEnv        = "NOVA_FLEET_PLAY_DIR"
	DefaultPlayDirRel = "rowan-working/rowan-tools/fleet"
	// PlayInventory is the dynamic inventory in the play directory: it
	// reads the machines registry FLEET_REGISTRY names.
	PlayInventory = "inventory.py"
	PlayRegistry  = "FLEET_REGISTRY"
	// PlayForks is the play's parallelism (the tools target's --forks 16).
	PlayForks = 16
	// PlayTimeout bounds one play; RestartTimeout the beat restart.
	PlayTimeout    = 15 * time.Minute
	RestartTimeout = 2 * time.Minute
	// DefaultVerifyWait is how long the verify re-reads the beats after the
	// play (a restarted beat writes its build within a tick or two);
	// DefaultVerifyPoll is the pause between reads.
	DefaultVerifyWait = 60 * time.Second
	DefaultVerifyPoll = 5 * time.Second
)

// BeatCheck is one bench's beat against the release.
type BeatCheck struct {
	Bench, Want string
	Have        string // the version the beat names; "" when the bench has no beat
}

// OK is true when the beat names the wanted version.
func (b BeatCheck) OK() bool { return b.Have != "" && b.Have == b.Want }

// Line is the VERIFY receipt: bench, want, have, ok|behind.
func (b BeatCheck) Line() string {
	word := "behind"
	if b.OK() {
		word = "ok"
	}
	have := b.Have
	if have == "" {
		have = "none"
	}
	return fmt.Sprintf("VERIFY %s want=%s have=%s %s", b.Bench, b.Want, have, word)
}

// VerifyBeats reads bench:<b>:beat build for every bench in one pipelined
// round trip and checks each against want. A bench with no beat has Have "".
func VerifyBeats(ctx context.Context, c *redis.Client, benches []string, want string) ([]BeatCheck, error) {
	if len(benches) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.StringCmd, len(benches))
	for i, b := range benches {
		cmds[i] = pipe.HGet(ctx, "bench:"+b+":beat", "build")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([]BeatCheck, len(benches))
	for i, b := range benches {
		out[i] = BeatCheck{Bench: b, Want: want, Have: beatVersion(cmds[i].Val())}
	}
	return out, nil
}

// behind names the checks whose beat is not on the release.
func behind(checks []BeatCheck) []string {
	var out []string
	for _, c := range checks {
		if !c.OK() {
			out = append(out, c.Bench)
		}
	}
	return out
}

// beatVersion is the version a beat's build field names: field two of a
// nova-sprint version line, else the field itself with no whitespace.
func beatVersion(line string) string {
	if bf, ok := buildinfo.Parse(line); ok {
		return bf.Version
	}
	return strings.Join(strings.Fields(line), "_")
}

// DevTipArgv asks the remote for dev's tip without a clone.
func DevTipArgv(repo string) []string {
	return []string{"git", "ls-remote", repo, "refs/heads/" + ReleaseBase}
}

// ParseDevTip reads the sha out of git ls-remote's one line.
func ParseDevTip(out string) (string, bool) {
	f := strings.Fields(lastLine(out))
	if len(f) != 2 || f[1] != "refs/heads/"+ReleaseBase || !commitRe.MatchString(f[0]) {
		return "", false
	}
	return f[0], true
}

// PlayArgv is the fleet play for version (none when "": the play's own
// declared build), narrowed to limit when given.
func PlayArgv(play, version string, limit []string) []string {
	argv := []string{"ansible-playbook", "-i", PlayInventory, play, "--forks", fmt.Sprint(PlayForks), "--diff"}
	if version != "" {
		argv = append(argv, "-e", "nova_build="+version)
	}
	if len(limit) > 0 {
		argv = append(argv, "--limit", strings.Join(limit, ","))
	}
	return argv
}

// PlayEnv is the play's environment: the Makefile's ansible settings and the
// registry the inventory reads.
func PlayEnv(registry string) []string {
	return []string{"ANSIBLE_NOCOWS=1", "ANSIBLE_HOST_KEY_CHECKING=True", PlayRegistry + "=" + registry}
}

// Recap is the PLAY RECAP section of ansible's output: one row per host
// (`<host> : ok=N changed=N unreachable=N failed=N ...`) with its runs of
// spaces folded. A line in the section that is not a host row (a timing
// callback's ROLES RECAP, a warning printed at the end) is not a row. Lines
// are read without a carriage return or colour codes.
func Recap(out string) []string {
	var rows []string
	in := false
	for _, l := range strings.Split(out, "\n") {
		l = plainLine(l)
		if strings.HasPrefix(l, "PLAY RECAP") {
			in = true
			continue
		}
		if in && recapRowRe.MatchString(l) {
			rows = append(rows, strings.Join(strings.Fields(l), " "))
		}
	}
	return rows
}

// RecapHosts is the host each Recap row names.
func RecapHosts(rows []string) map[string]bool {
	hosts := map[string]bool{}
	for _, r := range rows {
		if f := strings.Fields(r); len(f) > 0 {
			hosts[f[0]] = true
		}
	}
	return hosts
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// plainLine is an output line without its carriage return or ANSI colour.
func plainLine(l string) string {
	return ansiRe.ReplaceAllString(strings.TrimRight(l, "\r"), "")
}

// FirstLine is the first non-empty line of out, trimmed, without colour:
// what a refusal quotes when ansible printed nothing the parsers read.
func FirstLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(plainLine(l)); t != "" {
			return t
		}
	}
	return ""
}

// Unparseable is the refusal of an ansible run the parsers read nothing
// from: the first line it printed, quoted, or, when it printed nothing, how
// it exited.
func Unparseable(out string, err error) string {
	if l := FirstLine(out); l != "" {
		return fmt.Sprintf("ansible printed nothing parseable: %q", l)
	}
	exit := "exit status 0"
	if err != nil {
		exit = err.Error()
	}
	return "ansible printed nothing (" + exit + ")"
}

// BeatRestartScript restarts the bench beat whatever the bench's OS: the
// LaunchDaemon's kickstart on darwin, the systemd user unit on linux (the
// two RestartArgv lines a bench takes).
func BeatRestartScript() string {
	darwin, _ := RestartArgv("darwin", "system", BeatUnit, true)
	linux, _ := RestartArgv("linux", "", BeatUnit, false)
	return `if [ "$(uname)" = Darwin ]; then ` + strings.Join(darwin, " ") + `; else ` + strings.Join(linux, " ") + `; fi`
}

// RestartBeatsArgv restarts the beat on benches through ansible's ad hoc
// shell module, over the same inventory the play reads: one run, every bench
// at once, no ssh by hand.
func RestartBeatsArgv(benches []string) []string {
	return []string{"ansible", strings.Join(benches, ","), "-i", PlayInventory, "--forks", fmt.Sprint(PlayForks),
		"-m", "ansible.builtin.shell", "-a", BeatRestartScript()}
}

// adhocStatus are the status words of an ad hoc header line.
var adhocStatus = map[string]bool{"CHANGED": true, "SUCCESS": true, "FAILED": true, "FAILED!": true, "UNREACHABLE!": true}

// AdhocAnswer is what ansible's ad hoc output says of one host: its status
// word, the rc it names (in the header, `rc=5 >>`, or the JSON body's
// "rc"), and the first line of its message (the command's first output line
// under a `>>` header, the JSON body's "msg" under a `=> {` one).
type AdhocAnswer struct{ Status, RC, Msg string }

// Reason is the answer's rc and message for a BEAT line: " rc=5: <msg>",
// either part left out when ansible gave none.
func (a AdhocAnswer) Reason() string {
	s := ""
	if a.RC != "" {
		s = " rc=" + a.RC
	}
	if a.Msg != "" {
		s += ": " + a.Msg
	}
	return s
}

var (
	adhocMsgRe = regexp.MustCompile(`^\s*"msg":\s*("(?:[^"\\]|\\.)*")`)
	adhocRCRe  = regexp.MustCompile(`^\s*"rc":\s*(-?[0-9]+)`)
)

// adhocHeader reports whether l (plain) is a `<host> | <STATUS> ...` header
// line at the start of a line.
func adhocHeader(l string, f []string) bool {
	return len(f) >= 3 && f[1] == "|" && adhocStatus[f[2]] && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "[")
}

// AdhocAnswers reads ansible's ad hoc output: host -> its answer, from each
// `<host> | <STATUS> ...` header line at the start of a line and the lines
// under it. Everything else (the [ERROR]/[WARNING]/[DEPRECATION WARNING]
// blocks ansible 2.19 prints before a header, which also end the output of
// the header before them) is not a header.
func AdhocAnswers(out string) map[string]AdhocAnswer {
	ans := map[string]AdhocAnswer{}
	cur, inJSON := "", false
	for _, raw := range strings.Split(out, "\n") {
		l := plainLine(raw)
		f := strings.Fields(l)
		if len(f) >= 3 && adhocHeader(l, f) {
			a := AdhocAnswer{Status: f[2]}
			for _, w := range f[3:] {
				if strings.HasPrefix(w, "rc=") {
					a.RC = strings.TrimPrefix(w, "rc=")
				}
			}
			ans[f[0]] = a
			cur, inJSON = f[0], strings.HasSuffix(strings.TrimSpace(l), "=> {")
			continue
		}
		if cur == "" {
			continue
		}
		a := ans[cur]
		switch {
		case inJSON && strings.TrimSpace(l) == "}" && !strings.HasPrefix(l, " "):
			cur = ""
		case inJSON:
			if m := adhocMsgRe.FindStringSubmatch(l); m != nil && a.Msg == "" {
				if u, err := strconv.Unquote(m[1]); err == nil {
					a.Msg = FirstLine(u)
				}
			}
			if m := adhocRCRe.FindStringSubmatch(l); m != nil && a.RC == "" {
				a.RC = m[1]
			}
			ans[cur] = a
		case strings.HasPrefix(l, "["):
			cur = ""
		case strings.TrimSpace(l) != "":
			a.Msg = strings.TrimSpace(l)
			ans[cur] = a
			cur = ""
		}
	}
	return ans
}

// ParseAdhoc is each host's status word (CHANGED, SUCCESS, FAILED, FAILED!,
// UNREACHABLE!) of AdhocAnswers.
func ParseAdhoc(out string) map[string]string {
	st := map[string]string{}
	for h, a := range AdhocAnswers(out) {
		st[h] = a.Status
	}
	return st
}

// DefaultInventoryRegistry is the registry inventory.py reads when
// FLEET_REGISTRY is unset; InventoryShape is the row it reads (a row with
// fewer columns is dropped), InventoryTimeout bounds one --list.
const (
	DefaultInventoryRegistry = "~/rowan-working/queue/control/machines.tsv"
	InventoryShape           = "6 tab-separated columns (name ssh os/arch roles seat cores; a bench's roles hold bench)"
	InventoryTimeout         = 30 * time.Second
)

// InventoryArgv is `inventory.py --list` of the play directory dir, run by
// its path as ansible runs it.
func InventoryArgv(dir string) []string {
	return []string{filepath.Join(dir, PlayInventory), "--list"}
}

// InventoryBenches reads inventory.py --list: the hosts of its benches group
// (the group the play's hosts: names).
func InventoryBenches(out string) ([]string, error) {
	var inv struct {
		Benches struct {
			Hosts []string `json:"hosts"`
		} `json:"benches"`
	}
	if err := json.Unmarshal([]byte(out), &inv); err != nil {
		return nil, fmt.Errorf("not an inventory: %v: %q", err, FirstLine(out))
	}
	return inv.Benches.Hosts, nil
}
