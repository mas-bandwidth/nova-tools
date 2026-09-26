package fleetbuild

// The pieces of the roll (#4332) that fleet release (#4356 A) runs after the
// build: dev's tip, the bench play through ansible, the beat restart through
// ansible, and the verify from the beats. None of them reaches a host by
// ssh: the play and the restart go through the play directory's inventory
// (fleet-changes-only-through-ansible), the verify reads the store.

import (
	"context"
	"errors"
	"fmt"
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

// Recap is the PLAY RECAP section of ansible's output, one host per line
// with its runs of spaces folded.
func Recap(out string) []string {
	var rows []string
	in := false
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "PLAY RECAP") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if t := strings.Join(strings.Fields(l), " "); t != "" {
			rows = append(rows, t)
		}
	}
	return rows
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

// ParseAdhoc reads ansible's ad hoc output: host -> its status word
// (CHANGED, SUCCESS, FAILED, UNREACHABLE!) from each `<host> | <STATUS> ...`
// header line.
func ParseAdhoc(out string) map[string]string {
	st := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && f[1] == "|" && !strings.HasPrefix(l, " ") {
			st[f[0]] = f[2]
		}
	}
	return st
}
