package deal

import (
	"strings"
	"testing"
)

// TestRefusalWhysKeepTheLauncherLines is nova-tools #3700 part 2: a failed
// session's stdout from `card launch --stdin` gives the bench row the first
// REFUSED line verbatim (not the secrets banner on stderr), and each card
// its own REFUSED line by batch line; a card that launched gets none. A
// batch that never ran (no per-line answer) gives every card the row's why.
func TestRefusalWhysKeepTheLauncherLines(t *testing.T) {
	banner := "SECRETS EXEC OK as=ctl keys=1 only=1 required=1 cmd=nova-sprint\n"
	se := &SessionError{Bench: "ctl", State: SSHError, Exit: 1, Stderr: banner,
		Stdout: "LAUNCHED s/a/1 pid=9\nREFUSED line=3 wrapper s/c/1: NOPERM ws:*\nREFUSED line=2 start s/b/1: no such file\nLAUNCH started=1 refused=2 ms=4 over=false\n"}
	why, cards := refusalWhys(se, se.Error(), 3)
	if why != "REFUSED line=2 start s/b/1: no such file" {
		t.Fatalf("row why %q, want the first REFUSED line", why)
	}
	want := []string{"", "REFUSED line=2 start s/b/1: no such file", "REFUSED line=3 wrapper s/c/1: NOPERM ws:*"}
	if strings.Join(cards, "|") != strings.Join(want, "|") {
		t.Fatalf("card whys %q, want %q", cards, want)
	}

	never := &SessionError{Bench: "ctl", State: SSHError, Exit: 2, Stderr: banner + "nova-sprint card launch: wrapper /x MISSING\n"}
	why, cards = refusalWhys(never, never.Error(), 2)
	if why != "ssh: error: bench ctl exit 2: nova-sprint card launch: wrapper /x MISSING" {
		t.Fatalf("row why %q: the secrets banner is not a why", why)
	}
	if cards[0] != why || cards[1] != why {
		t.Fatalf("card whys %q, want the row's why on every card of a batch that never ran", cards)
	}
	if got := stderrLine(banner); got != strings.TrimSpace(banner) {
		t.Fatalf("stderr of only the banner reads %q, want the banner itself", got)
	}
}
