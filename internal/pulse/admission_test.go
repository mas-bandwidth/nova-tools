package pulse

import "testing"

// The pit stop's admission list (rule C of #828): while a STOP file exists, launch admits
// only cards whose line 1 names the issue or test on STOP's second line, and refuses the
// rest. These tests exercise the pure gate Admit alone; the launch wiring is guarded by the
// existing launch tests, which never write a STOP and so must keep admitting every card.

func TestAdmitStopNamesIssue(t *testing.T) {
	stop := []byte("# pit stop\n#694\n")
	if ok, _ := Admit(stop, "#694 fixed"); !ok {
		t.Fatalf("card line 1 %q under STOP %q: not admitted, want admitted", "#694 fixed", "#694")
	}
	if ok, _ := Admit(stop, "#695 fixed"); ok {
		t.Fatalf("card line 1 %q not naming %q: admitted, want refused", "#695 fixed", "#694")
	}
}

func TestAdmitNoStopAdmitsAll(t *testing.T) {
	for _, stop := range [][]byte{nil, {}} {
		if ok, reason := Admit(stop, "anything"); !ok || reason != "" {
			t.Fatalf("no STOP should admit all, got ok=%v reason=%q", ok, reason)
		}
	}
}

func TestAdmitStopWithoutSecondLine(t *testing.T) {
	ok, reason := Admit([]byte("# pit stop\n"), "#694 fixed")
	if ok {
		t.Fatalf("STOP with no second line admitted a card, want refused")
	}
	if reason == "" {
		t.Fatalf("STOP with no second line prints no reason")
	}
}
