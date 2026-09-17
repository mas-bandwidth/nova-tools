package merge

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Wait polls the SAME host reader the lane uses for PR state and checks --
// Host.PR and Host.Checks -- until the pull request is merged, is red, or the
// timeout runs out. It exists so a coordinator blocks in one full-context turn
// instead of watching GitHub by hand.
//
// Exactly one line goes to stdout, and nothing is printed between polls. The
// exit codes are the ones the verb promises: 0 merged, 2 red (a failing check
// or a closed-unmerged PR), 3 the timeout. Host errors are not an ending: a
// read that fails is retried on the next poll until the timeout says otherwise.
func Wait(host Host, prNum int, timeout, interval time.Duration, now func() time.Time, sleep func(time.Duration), stdout io.Writer) int {
	start := now()
	deadline := start.Add(timeout)
	wall := func() int {
		s := int(now().Sub(start).Seconds())
		if s < 0 {
			return 0
		}
		return s
	}
	state, pending := "open", "-"
	for {
		if pr, err := host.PR(prNum); err == nil {
			if pr.Merged {
				sha := pr.MergeSHA
				if sha == "" {
					sha = pr.HeadOID
				}
				if sha == "" {
					sha = "-"
				}
				// nova-tools #229 point 5: a merge line names its base, and only a
				// base of main is a landing. #144 merged onto its stack
				// (docs-migration), not main; without the base the MERGED line
				// reads as a landing. The base is therefore on the line, and a
				// merge onto any other base is a stack merge, not a landing.
				base := strings.TrimSpace(pr.Base)
				if base == "" {
					base = "-"
				} else {
					base = oneline.Field(base)
				}
				fmt.Fprintf(stdout, "MERGE WAIT MERGED pr=%d base=%s sha=%s wall=%d\n", prNum, base, sha, wall())
				return 0
			}
			if checks, err := host.Checks(pr.HeadOID); err == nil {
				pending = checks.PendingList()
				if checks.Red > 0 {
					names := append([]string(nil), checks.RedNames...)
					sort.Strings(names)
					name := names[0]
					fmt.Fprintf(stdout, "MERGE WAIT RED pr=%d check=%s conclusion=%s wall=%d\n",
						prNum, name, checks.ConclusionFor(name), wall())
					return 2
				}
			}
			if pr.Closed {
				fmt.Fprintf(stdout, "MERGE WAIT RED pr=%d check=- conclusion=closed wall=%d\n", prNum, wall())
				return 2
			}
			state = "open"
		}
		if !now().Before(deadline) {
			fmt.Fprintf(stdout, "MERGE WAIT TIMEOUT pr=%d state=%s pending=%s wall=%d\n",
				prNum, state, pending, wall())
			return 3
		}
		sleep(interval)
	}
}
