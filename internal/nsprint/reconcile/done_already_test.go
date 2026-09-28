package reconcile_test

import (
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

func TestDoneAlreadyParsers(t *testing.T) {
	t.Parallel()

	for line, want := range map[string]string{
		"ABSTAIN done-already d20d73c4":           "d20d73c4",
		"ABSTAIN done-already D20D73C4 via #3901": "d20d73c4",
		"ABSTAIN done-already abc":                "",
		"ABSTAIN out of scope":                    "",
		"DONE":                                    "",
	} {
		if got := reconcile.DoneAlreadySHA(line); got != want {
			t.Errorf("DoneAlreadySHA(%q) = %q, want %q", line, got, want)
		}
	}
	for origin, want := range map[string]string{
		"https://forge.test/mas-bandwidth/nova-tools/issues/3919": "mas-bandwidth/nova-tools#3919",
		"mas-bandwidth/rowan-tools#12":                            "mas-bandwidth/rowan-tools#12",
		"nova-work:item-7":                                        "",
		"":                                                        "",
	} {
		repo, n, ok := reconcile.OriginIssue(origin)
		got := ""
		if ok {
			got = repo + "#" + strconv.Itoa(n)
		}
		if got != want {
			t.Errorf("OriginIssue(%q) = %q, want %q", origin, got, want)
		}
	}
}
