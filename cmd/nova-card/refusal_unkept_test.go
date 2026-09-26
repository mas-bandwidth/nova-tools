package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestARefusalThatCannotBeKeptSaysSoOnStderr: with Redis down and no
// absolute NOVA_CARD_RESULTS (the configuration refusal itself, exit 1), a
// refusal before launched had nowhere to go and the wrapper said nothing
// about it. It prints one stderr line naming the card and every place the
// record could not be kept: the dial and the results root (Glenn
// 2026-09-26: no verb on the live path fails silently).
func TestARefusalThatCannotBeKeptSaysSoOnStderr(t *testing.T) {
	t.Parallel()
	down := closedAddr(t)
	env := map[string]string{
		"NOVA_CARD_BENCH": "bench-one", "NOVA_CARD_HARNESS": "/bin/true",
		"NOVA_CARD_JOBS": t.TempDir(), "NOVA_CARD_RESULTS": "relative/results",
		"NOVA_CARD_CLOCK": "45m", "NOVA_CARD_REDIS": down,
	}
	const cardName = "s3420/card-unkept/1"
	var out, errb strings.Builder
	code := run([]string{cardName}, strings.NewReader("s3420 card-unkept 1 "+testToken+"\n"), &out, &errb,
		func(k string) string { return env[k] })
	if code != card.WrapperExitUsage {
		t.Fatalf("exit %d, want %d; stdout %q stderr %q", code, card.WrapperExitUsage, out.String(), errb.String())
	}
	line := errb.String()
	if !strings.HasPrefix(line, "nova-card: refusal of "+cardName+" not recorded: xadd s:s3420:log: ") ||
		!strings.Contains(line, "connection refused") ||
		!strings.Contains(line, "NOVA_CARD_RESULTS is not an absolute path") {
		t.Fatalf("stderr %q does not name the card and where the record could not go", line)
	}
	if strings.Contains(line, "0123456789abcdef0123456789abcdef") {
		t.Fatalf("stderr carries the token: %q", line)
	}
}
