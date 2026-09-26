package launch

import (
	"fmt"
	"strings"
	"time"
)

// A consumer copy's launch (#3998): the bench's session start takes its
// copies with one `card work --as bench:<b> --fill` and starts one wrapper
// per copy, detached exactly as a sprint card's (own session, stdout and
// stderr /dev/null, the acknowledgement on fd 3), with the command identity
// `nova-card copy <copy>` and the one stdin line `<copy> <token>`. The token
// is never an argument.

// CopyArg is argv[1] of a copy's wrapper.
const CopyArg = "copy"

// CopyLine is one copy to start.
type CopyLine struct{ Copy, Token string }

// CommandIdentity is what `ps -o command=` shows for this copy's wrapper.
func (l CopyLine) CommandIdentity() string { return WrapperName + " " + CopyArg + " " + l.Copy }

// String is the stdin line, token included; never print it.
func (l CopyLine) String() string { return l.Copy + " " + l.Token }

// ParseCopyLine reads `<copy> <token>`: two fields, the copy <primary>~<n>.
func ParseCopyLine(s string) (CopyLine, error) {
	f := strings.Fields(s)
	if len(f) != 2 || strings.TrimSpace(s) != f[0]+" "+f[1] {
		return CopyLine{}, fmt.Errorf("a copy line is <copy> <token>")
	}
	i := strings.LastIndex(f[0], "~")
	if i <= 0 || i == len(f[0])-1 || strings.Trim(f[0][i+1:], "0123456789") != "" {
		return CopyLine{}, fmt.Errorf("%q is not a copy id <primary>~<n>", f[0])
	}
	return CopyLine{Copy: f[0], Token: f[1]}, nil
}

// LaunchCopy starts one copy's wrapper and waits for its acknowledgement
// (LAUNCHED, or its REFUSED line) until budget runs out.
func LaunchCopy(wrapper string, l CopyLine, budget time.Duration) (string, error) {
	return LaunchCopyEnv(wrapper, l, budget, nil)
}

// LaunchCopyEnv is LaunchCopy with the bench's card environment for the
// wrapper (NOVA_CARD_* and the seat's Redis identity: the play writes it to
// ~/nova-bench/launch/card.env; the beat reads it with ReadEnvFile and hands
// it to the child, never to itself). Without it nova-card copy refuses at
// once, "missing or bad NOVA_CARD_BENCH, ...", the first copy-model quack's
// launch failure (2026-09-25 11:21 PM ET).
func LaunchCopyEnv(wrapper string, l CopyLine, budget time.Duration, env []string) (string, error) {
	if err := checkWrapper(wrapper); err != nil {
		return "", err
	}
	if budget <= 0 {
		budget = DefaultBudget
	}
	_, ack, err := startDetachedArgsEnv(wrapper, []string{WrapperName, CopyArg, l.Copy}, l.String(), time.Now().Add(budget), env)
	if err != nil {
		return "", err
	}
	if ack != "LAUNCHED" {
		return ack, fmt.Errorf("%s", ack)
	}
	return ack, nil
}
