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
