package fleetbuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The play answers (#4383 fix): fleet release reports what the play and
// the beat restart did, as ansible said it. The fixtures:
//
//	ansible-play-2026-09-26.log          the real output of the play step's argv
//	                                     run by hand on the Studio, 2026-09-26
//	ansible-play-empty-inventory.log     the play over an inventory with no
//	                                     benches: what the release got when
//	                                     FLEET_REGISTRY named a registry
//	                                     inventory.py reads no row of (the
//	                                     message strings are ansible-core's own)
//	ansible-adhoc-2.19.log               ansible 2.19's ad hoc shape: an
//	                                     [ERROR] block before each header
//	ansible-adhoc-empty-inventory.log    the ad hoc restart over that inventory

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// realRecap is the PLAY RECAP of the real fixture, folded.
var realRecap = []string{
	"batman : ok=18 changed=3 unreachable=0 failed=0 skipped=5 rescued=0 ignored=0",
	"hetzner : ok=19 changed=3 unreachable=0 failed=0 skipped=4 rescued=0 ignored=0",
	"hulk : ok=21 changed=5 unreachable=0 failed=0 skipped=5 rescued=0 ignored=0",
	"space : ok=17 changed=1 unreachable=0 failed=0 skipped=6 rescued=0 ignored=0",
	"superman : ok=18 changed=3 unreachable=0 failed=0 skipped=5 rescued=0 ignored=0",
	"vision : ok=18 changed=3 unreachable=0 failed=0 skipped=5 rescued=0 ignored=0",
}

// TestRecapReadsTheRealPlay: Recap over the real output is its six RECAP
// rows; the same output with carriage returns and colour codes reads the
// same; the timing callback's ROLES RECAP is not a row; an empty inventory's
// play has a PLAY RECAP header and no row.
func TestRecapReadsTheRealPlay(t *testing.T) {
	t.Parallel()
	real := fixture(t, "ansible-play-2026-09-26.log")
	if got := Recap(real); strings.Join(got, "\n") != strings.Join(realRecap, "\n") {
		t.Fatalf("recap of the real play:\n%s", strings.Join(got, "\n"))
	}
	coloured := strings.ReplaceAll(real, "\n", "\r\n")
	coloured = strings.Replace(coloured, "PLAY RECAP", "\x1b[0;33mPLAY RECAP", 1)
	coloured = strings.Replace(coloured, "batman ", "\x1b[0;33mbatman\x1b[0m ", -1)
	if got := Recap(coloured); strings.Join(got, "\n") != strings.Join(realRecap, "\n") {
		t.Errorf("recap with \\r and colour:\n%s", strings.Join(got, "\n"))
	}
	if got := Recap(fixture(t, "play-recap.txt")); len(got) != 4 || !strings.HasPrefix(got[3], "space : ok=5") {
		t.Errorf("recap with a ROLES RECAP after it: %q", got)
	}
	if got := Recap(fixture(t, "ansible-play-empty-inventory.log")); got != nil {
		t.Errorf("recap of an empty inventory: %q", got)
	}
	if h := RecapHosts(realRecap); len(h) != 6 || !h["hetzner"] || h["studio"] {
		t.Errorf("hosts %v", h)
	}
}

// TestParseAdhocReadsTheHeaders: `host | STATUS` headers are read past
// ansible 2.19's [ERROR] and [DEPRECATION WARNING] blocks; the empty
// inventory's warnings are no header.
func TestParseAdhocReadsTheHeaders(t *testing.T) {
	t.Parallel()
	st := ParseAdhoc(fixture(t, "ansible-adhoc-2.19.log"))
	if len(st) != 3 || st["superman"] != "FAILED" || st["space"] != "FAILED" || st["hulk"] != "CHANGED" {
		t.Errorf("2.19 shape: %v", st)
	}
	if st := ParseAdhoc(strings.ReplaceAll("\x1b[0;31mbatman | UNREACHABLE! => {\x1b[0m\n    \"msg\": \"x | FAILED | y\"\n}\n", "\n", "\r\n")); len(st) != 1 || st["batman"] != "UNREACHABLE!" {
		t.Errorf("coloured: %v", st)
	}
	if st := ParseAdhoc(fixture(t, "ansible-adhoc-empty-inventory.log")); len(st) != 0 {
		t.Errorf("empty inventory: %v", st)
	}
}

// TestUnparseableNamesTheFirstLine: the refusal quotes the first non-empty
// line, or says how ansible exited when it printed nothing.
func TestUnparseableNamesTheFirstLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		out  string
		err  error
		want string
	}{
		{fixture(t, "ansible-play-empty-inventory.log"), nil, `ansible printed nothing parseable: "[WARNING]: provided hosts list is empty, only localhost is available. Note that the implicit localhost does not match 'all'"`},
		{"\n\r\n  \x1b[0;31mERROR! the playbook: tools.yml could not be found\x1b[0m\n", errors.New("exit status 1"), `ansible printed nothing parseable: "ERROR! the playbook: tools.yml could not be found"`},
		{"", nil, "ansible printed nothing (exit status 0)"},
		{"\n", errors.New(`exec: "ansible": executable file not found in $PATH`), `ansible printed nothing (exec: "ansible": executable file not found in $PATH)`},
	} {
		if got := Unparseable(tc.out, tc.err); got != tc.want {
			t.Errorf("Unparseable = %s\nwant          %s", got, tc.want)
		}
	}
}

// TestReleasePlayAnswersAsAnsibleSaid runs the play step over the fixtures:
// the real play prints its six RECAP rows and answers OK; the play over an
// empty inventory, and a play that prints nothing, are one typed refusal
// naming ansible's first line (never "no answer" per bench); a bench the
// RECAP lacks is named; an unparseable restart is one refusal; the raw
// output is kept in a file the receipt names.
func TestReleasePlayAnswersAsAnsibleSaid(t *testing.T) {
	t.Parallel()
	real := fixture(t, "ansible-play-2026-09-26.log")
	emptyPlay := fixture(t, "ansible-play-empty-inventory.log")
	emptyAdhoc := fixture(t, "ansible-adhoc-empty-inventory.log")
	nothing := ""
	partial := strings.Replace(rollRecap, "batman                     : ok=9    changed=1    unreachable=0    failed=0\n", "", 1)
	for _, tc := range []struct {
		name        string
		play, adhoc *string
		state       string
		want        []string
		log         *string // the play log's content
	}{
		{"the real play", &real, nil, "OK", append(func() []string {
			var l []string
			for _, r := range realRecap {
				l = append(l, "RECAP "+r+"\n")
			}
			return l
		}(), "BEAT hulk restarted\nBEAT batman restarted\n", "RELEASE play OK tools.yml version="+relVersion+" hosts=6 beats-restarted=2 log="), &real},
		{"empty inventory", &emptyPlay, &emptyAdhoc, "REFUSED", []string{
			`RELEASE play REFUSED: ansible printed nothing parseable: "[WARNING]: provided hosts list is empty, only localhost is available. Note that the implicit localhost does not match 'all'" (the play reached none of space,hulk,batman; the inventory read FLEET_REGISTRY=/reg/machines.tsv; ansible's output: `,
		}, &emptyPlay},
		{"nothing printed", &nothing, &nothing, "REFUSED", []string{
			"RELEASE play REFUSED: ansible printed nothing (exit status 0) (the play reached none of space,hulk,batman;",
		}, &nothing},
		{"a bench not in the recap", &partial, nil, "REFUSED", []string{
			"RECAP hulk : ok=9", "BEAT batman restarted\n",
			"RELEASE play REFUSED: the play's RECAP has no batman (the inventory read FLEET_REGISTRY=/reg/machines.tsv: does it list them? ansible's output: ",
		}, &partial},
		{"restart unparseable", &real, &emptyAdhoc, "REFUSED", []string{
			`RELEASE play REFUSED: the beat restart of hulk,batman: ansible printed nothing parseable: "[WARNING]: provided hosts list is empty, only localhost is available. Note that the implicit localhost does not match 'all'" (ansible's output: `,
		}, &real},
	} {
		mr := relStore(t)
		f := &relFake{home: t.TempDir(), playOut: tc.play, adhocOut: tc.adhoc}
		f.restarted = catchUp(mr)
		r, out, _ := newRelease(t, f, mr)
		res, err := r.Run(context.Background(), relSha)
		s := out.String()
		if err != nil || !strings.Contains(states(res), "play="+tc.state+" ") {
			t.Errorf("%s: err=%v states=%s\n%s", tc.name, err, states(res), s)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(s, w) {
				t.Errorf("%s: missing %q in\n%s", tc.name, w, s)
			}
		}
		if strings.Contains(s, "no answer") {
			t.Errorf("%s: a bench reads \"no answer\":\n%s", tc.name, s)
		}
		logPath := filepath.Join(f.home, "nova-bench", "release-src", "logs", "play-"+relVersion+".log")
		if b, err := os.ReadFile(logPath); err != nil || string(b) != *tc.log {
			t.Errorf("%s: the play log %s: %v (%d bytes)", tc.name, logPath, err, len(b))
		}
		var step string
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, "RELEASE play ") {
				step = l
			}
		}
		wantLog := logPath
		if strings.Contains(tc.name, "restart") {
			wantLog = filepath.Join(f.home, "nova-bench", "release-src", "logs", "beat-restart-"+relVersion+".log")
		}
		if !strings.Contains(step, wantLog) || !strings.Contains(s, "ANSIBLE LOG "+wantLog+" bytes=") {
			t.Errorf("%s: the receipt does not name %s: %q", tc.name, wantLog, step)
		}
	}
}

// TestParseAdhocIgnoresTheCommandsOwnLines: a line the restarted command
// prints at the start of a line is no header, though it reads `<word> |
// <word>`: one naming a host with a word that is not a status (dev's rule
// read it as space's status "restarted"), one opening with `[` (dev's rule
// read it as a host "[hulk]").
func TestParseAdhocIgnoresTheCommandsOwnLines(t *testing.T) {
	t.Parallel()
	out := "space | FAILED | rc=5 >>\n" +
		"Failed to restart nova-loop-nova-sprint-bench-beat.service: Unit nova-loop-nova-sprint-bench-beat.service not found.\n" +
		"hulk | CHANGED | rc=0 >>\n" +
		"space | restarted by hand before this run\n" +
		"[hulk] | FAILED | a tag the command printed\n"
	st := ParseAdhoc(out)
	if len(st) != 2 || st["space"] != "FAILED" || st["hulk"] != "CHANGED" {
		t.Errorf("adhoc %v", st)
	}
}

// TestReleaseSkipsTheRestartWhenThePlayReachedNone: a play whose RECAP has
// no row starts no beat restart; a BEAT line says it was skipped and the
// play's refusal is the step's answer.
func TestReleaseSkipsTheRestartWhenThePlayReachedNone(t *testing.T) {
	t.Parallel()
	emptyPlay := fixture(t, "ansible-play-empty-inventory.log")
	adhoc := "hulk | CHANGED | rc=0 >>\n\nbatman | CHANGED | rc=0 >>\n\n"
	mr := relStore(t)
	f := &relFake{home: t.TempDir(), playOut: &emptyPlay, adhocOut: &adhoc}
	r, out, _ := newRelease(t, f, mr)
	res, err := r.Run(context.Background(), relSha)
	s := out.String()
	if err != nil || !strings.Contains(states(res), "play=REFUSED ") {
		t.Fatalf("err=%v states=%s\n%s", err, states(res), s)
	}
	if _, ran := f.find("ansible "); ran {
		t.Errorf("the beat restart ran:\n%s", strings.Join(f.heads(), "\n"))
	}
	for _, w := range []string{
		"BEAT skipped hulk,batman: the play reached no bench\n",
		"(the play reached none of space,hulk,batman; the inventory read FLEET_REGISTRY=/reg/machines.tsv;",
	} {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in\n%s", w, s)
		}
	}
	if strings.Contains(s, "BEAT hulk") || strings.Contains(s, "beat-restart-") {
		t.Errorf("a restart answer printed:\n%s", s)
	}
}

// TestReleaseChecksTheInventoryBeforeThePlay: inventory.py --list runs in
// the play directory over the play's registry before any ansible; an
// inventory with no bench, one without a bench of the roll, a failing
// inventory.py and one printing no inventory are each refused naming the
// row shape inventory.py reads and the registry it reads unset, and neither
// the play nor the restart runs; the verify still runs.
func TestReleaseChecksTheInventoryBeforeThePlay(t *testing.T) {
	t.Parallel()
	fix := " (inventory.py reads rows of 6 tab-separated columns (name ssh os/arch roles seat cores; a bench's roles hold bench) and drops the rest; unset, FLEET_REGISTRY is ~/rowan-working/queue/control/machines.tsv (--machines <that registry>)"
	empty := `{"benches": {"hosts": []}, "store": {"hosts": []}, "coordinator": {"hosts": []}, "_meta": {"hostvars": {}}}` + "\n"
	two := `{"benches": {"hosts": ["space", "hulk"]}}` + "\n"
	trace := "Traceback (most recent call last):\n  File \"inventory.py\", line 21\n"
	for _, tc := range []struct {
		name string
		inv  *string
		fail string
		want []string
	}{
		{"no bench", &empty, "", []string{"INVENTORY hosts=0 registry=/reg/machines.tsv\n",
			"RELEASE play REFUSED: inventory.py --list names no bench from FLEET_REGISTRY=/reg/machines.tsv" + fix + ")\n"}},
		{"a bench absent", &two, "", []string{"INVENTORY hosts=2 registry=/reg/machines.tsv\n",
			"RELEASE play REFUSED: inventory.py --list has no batman among its 2 benches from FLEET_REGISTRY=/reg/machines.tsv" + fix + "; a bench FLEET_STATE marks DOWN is dropped too)\n"}},
		{"inventory.py fails", nil, "inventory.py", []string{
			"RELEASE play REFUSED: inventory.py --list with FLEET_REGISTRY=/reg/machines.tsv: exit status 1: boom" + fix + ")\n"}},
		{"no inventory printed", &trace, "", []string{
			`RELEASE play REFUSED: inventory.py --list with FLEET_REGISTRY=/reg/machines.tsv: not an inventory: invalid character 'T' looking for beginning of value: "Traceback (most recent call last):"` + fix + ")\n"}},
	} {
		mr := relStore(t)
		f := &relFake{home: t.TempDir(), invOut: tc.inv, fail: tc.fail}
		f.restarted = catchUp(mr)
		r, out, _ := newRelease(t, f, mr)
		res, err := r.Run(context.Background(), relSha)
		s := out.String()
		if err != nil || states(res) != "build=OK fn=OK fn-check=OK play=REFUSED self=OK" || len(res.Checks) != 3 {
			t.Errorf("%s: err=%v states=%s checks=%d\n%s", tc.name, err, states(res), len(res.Checks), s)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(s, w) {
				t.Errorf("%s: missing %q in\n%s", tc.name, w, s)
			}
		}
		if heads := strings.Join(f.heads(), "\n"); strings.Contains(heads, "ansible") {
			t.Errorf("%s: ansible ran:\n%s", tc.name, heads)
		}
		c, ok := f.find(filepath.Join(r.PlayDir, PlayInventory) + " --list")
		if !ok || c.dir != r.PlayDir || strings.Join(c.env, " ") != "ANSIBLE_NOCOWS=1 ANSIBLE_HOST_KEY_CHECKING=True FLEET_REGISTRY=/reg/machines.tsv" {
			t.Errorf("%s: inventory call %v %+v", tc.name, ok, c)
		}
	}
}

// TestBeatLineNamesAnsiblesReason: AdhocAnswers reads each host's rc and
// first message line (under a `>>` header, the command's first line; under
// a `=> {` one, the JSON's "rc" and "msg"), and a failed BEAT line prints
// them: `BEAT hulk failed failed rc=5: Failed to restart ...`, not `failed
// failed` alone.
func TestBeatLineNamesAnsiblesReason(t *testing.T) {
	t.Parallel()
	a := AdhocAnswers(fixture(t, "ansible-adhoc-2.19.log"))
	for h, want := range map[string]AdhocAnswer{
		"space":    {"FAILED", "5", "Failed to restart nova-loop-nova-sprint-bench-beat.service: Unit nova-loop-nova-sprint-bench-beat.service not found."},
		"superman": {"FAILED", "113", `Could not find service "com.nova.loop.nova-sprint-bench-beat" in domain for system`},
		"hulk":     {"CHANGED", "0", ""},
	} {
		if a[h] != want {
			t.Errorf("%s: %+v, want %+v", h, a[h], want)
		}
	}
	json := AdhocAnswers("batman | UNREACHABLE! => {\n    \"changed\": false,\n    \"msg\": \"Failed to connect to the host via ssh: ssh: connect to host batman port 22: Operation timed out\",\n    \"unreachable\": true\n}\n" +
		"vision | FAILED! => {\n    \"changed\": true,\n    \"msg\": \"non-zero return code\\nsecond line\",\n    \"rc\": 5\n}\n")
	if got := json["batman"]; got != (AdhocAnswer{"UNREACHABLE!", "", "Failed to connect to the host via ssh: ssh: connect to host batman port 22: Operation timed out"}) {
		t.Errorf("batman %+v", got)
	}
	if got := json["vision"]; got != (AdhocAnswer{"FAILED!", "5", "non-zero return code"}) || got.Reason() != " rc=5: non-zero return code" {
		t.Errorf("vision %+v %q", got, got.Reason())
	}

	adhoc := "hulk | FAILED | rc=5 >>\nFailed to restart nova-loop-nova-sprint-bench-beat.service: Unit nova-loop-nova-sprint-bench-beat.service not found.\n" +
		"batman | CHANGED | rc=0 >>\n\n"
	mr := relStore(t)
	f := &relFake{home: t.TempDir(), adhocOut: &adhoc}
	r, out, _ := newRelease(t, f, mr)
	if _, err := r.Run(context.Background(), relSha); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"BEAT hulk failed failed rc=5: Failed to restart nova-loop-nova-sprint-bench-beat.service: Unit nova-loop-nova-sprint-bench-beat.service not found.\n",
		"BEAT batman restarted\n",
		"RELEASE play REFUSED: the beat restart failed on hulk (read the BEAT lines and ",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("missing %q in\n%s", w, out.String())
		}
	}
}
