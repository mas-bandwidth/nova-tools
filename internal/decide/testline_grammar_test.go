package decide

import "testing"

// TestWorkTypeReadsTESTThroughParseTest is the nova-tools#4401 read's item
// 3: the work-type router's testNone reads TEST through cardhdr.ParseTest,
// the one TEST grammar, so `none <why>` (the only none a card may carry) is a
// card that demands no test, like a bare none or no TEST line; a named test,
// tagged or not, is not.
func TestWorkTypeReadsTESTThroughParseTest(t *testing.T) {
	t.Parallel()
	for card, want := range map[string]bool{
		"KIND: read\nTEST: none the card reads a transcript\n": true,
		"KIND: read\nTEST: none\n":                             true,
		"KIND: read\n":                                         true,
		"KIND: build\nTEST: ./internal/x TestY\n":              false,
		"KIND: build\nTEST: -tags functional ./x TestY\n":      false,
	} {
		if got := readCardHeader(card).testNone(); got != want {
			t.Errorf("testNone(%q) = %v, want %v", card, got, want)
		}
	}
}
