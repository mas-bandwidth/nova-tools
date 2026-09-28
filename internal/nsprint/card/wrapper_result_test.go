package card_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestTestCommandIsTheDeclaredGrammar: only `<package> <TestName>` runs; the
// wrapper never assumes a package or runs a free command.
func TestTestCommandIsTheDeclaredGrammar(t *testing.T) {
	t.Parallel()

	for test, want := range map[string]string{
		"./internal/docs TestEveryTopLevelDocLinkResolves": "go test ./internal/docs -run ^TestEveryTopLevelDocLinkResolves$ -count=1",
		"./internal/pulse/ TestStale":                      "go test ./internal/pulse/ -run ^TestStale$ -count=1",
		". TestPass":                                       "go test . -run ^TestPass$ -count=1",
		"":                                                 "",
		"none":                                             "",
		"rm -rf /":                                         "",
		"../x TestY":                                       "",
		"./a/../../b TestY":                                "",
		"./x TestY; echo":                                  "",
		"./x NotATest":                                     "",
		"./.. TestY":                                       "",
	} {
		argv, why := card.TestCommand(test)
		if got := strings.Join(argv, " "); got != want || (want == "" && why == "") {
			t.Errorf("TestCommand(%q) = %q (why %q), want %q", test, got, why, want)
		}
	}
}
