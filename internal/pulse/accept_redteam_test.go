package pulse

// The red team's two escapes from the read of T03 at 98e3f3a9
// (tmp/session-0919b/redteam-launch/REPORT.md), each written here with the reader's own
// receipt as the test.
//
// #1806 (PROBE-A). `acceptToolchainRed` scored a wall marker printed BEFORE the first
// `=== RUN` as the bench's fault. A card's own TestMain or package init runs before any
// `=== RUN`, so a card could print `Operation not permitted`, fail, and be scored
// `ABSTAIN toolchain` instead of `REJECT red-at-head` -- dodging the rejection AND the
// track-record fail, and staling the bench's certification record on the way out.
//
// The rule now: output a process the CARD controls is never the bench's voice. When the
// card's code runs, the wall speaks through its exit code and through nothing else
// (nova-sandbox's 125, 126, 127). For `go build` and `go vet` the output is the
// toolchain's own and the text still counts, with the `# pkg` and `[build failed]`
// carve-outs that keep a compiler error naming WALL the card's.

import (
	"strings"
	"testing"
)

// exitErr and the unit test of acceptToolchainRed live in accept_toolchain_test.go
// (dev carried the #1806 fix ahead of this gate).

// The end-to-end receipt: a card whose TestMain prints the wall's words and whose test
// then fails is REJECTed, not abstained.
func TestAcceptAWallMarkerFromTestMainIsStillTheCardsRed(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", strings.Replace(baseTest, "import \"testing\"", "import (\n\t\"fmt\"\n\t\"os\"\n\t\"testing\"\n)", 1)+
		"\nfunc TestMain(m *testing.M) {\n\tfmt.Println(\"SANDBOX DENIED reason=fake: Operation not permitted\")\n\tos.Exit(m.Run())\n}\n"+
		"\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 1 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "a TestMain that talks like the wall before any test runs")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=red-at-head ")
}

// #1807 (PROBE-B), end to end: the card adds a new symbol and a test that only CALLS it.
// Reverting the fix deletes the symbol, the package stops compiling, and the gate used to
// read `[build failed]` as the kill its negative control was looking for -- so a test that
// asserts nothing reached `ACCEPT OK`. It is a rejection, and the token is the honest one:
// the test is vacuous.
//
// The cost of this rule, said out loud: a card that adds a NEW symbol with a genuinely
// asserting test is rejected the same way, because `mutate`'s revert cannot tell the two
// apart from the outside. That shape wants `mutation-kill`'s seed form (T13) or a spec
// answer on #1637; it is not something this gate can decide by looking.
func TestAcceptRejectsACallOnlyTestCoupledByCompilation(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix+"\n// Mul is new in this change, and it is wrong.\nfunc Mul(a, b int) int { return 0 }\n")
	acceptWrite(t, l.job, "sign/sign_test.go", baseTest+"\nfunc TestSignZero(t *testing.T) {\n\t_ = Mul(2, 3)\n}\n")
	l.commit(t, "a call-only test coupled to the fix by compilation")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=vacuous-test ")
}
