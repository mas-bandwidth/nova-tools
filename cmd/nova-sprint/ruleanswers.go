package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// rules: what the tick's rules would answer, now, read-only (sprint.RuleAnswers;
// docs/SPEC-SPRINT.md section 8, answered by rule). One RULE line per open judgment and
// subject: the rule and its act, or left (it needs a mind) or off (its rule is turned off
// in nova-config's sprint row), with why; one IDLE line, the fleet's working and width, the
// waiting cards and the roots they are behind, what the idle alarm would say; then one
// RULES line of counts by rule and act.
// It writes nothing: the run loop's tick applies the same answers (run --answer-rules).
func (a *app) cmdRules(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("rules")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "rules", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "rules", err.Error())
	}
	answers, idle, err := st.RuleAnswers(context.Background())
	if err != nil {
		return a.readFailed("rules", err, stderr)
	}
	counts := map[string]int{}
	acting, left, off := 0, 0, 0
	for _, x := range answers {
		switch {
		case x.Answers():
			acting++
			counts[x.Rule+" "+x.Act]++
		case x.Act == sprint.ActOff:
			off++
		default:
			left++
		}
	}
	var by []string
	for k, n := range counts {
		by = append(by, fmt.Sprintf("%s=%d", strings.ReplaceAll(k, " ", "_"), n))
	}
	sort.Strings(by)
	if c.json {
		if answers == nil {
			answers = []sprint.RuleAnswer{}
		}
		b, _ := json.Marshal(map[string]any{"answers": answers, "acting": acting, "left": left, "off": off, "by": counts,
			"idle": map[string]any{"working": idle.Working, "width": idle.Width, "waiting": idle.Waiting, "since": idle.Since, "roots": idle.Trace}})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	shown := 0
	for _, x := range answers {
		if c.max > 0 && shown == c.max {
			break
		}
		shown++
		fmt.Fprintf(stdout, "RULE %s type=%s subject=%s card=%s rule=%s act=%s waited=%s why=%s\n", oneline.Field(x.Judgment), oneline.Field(x.Type),
			oneline.Field(x.Subject), oneline.Field(dashed(x.Card)), oneline.Field(dashed(x.Rule)), oneline.Field(x.Act), oneline.Field(x.Waited), oneline.Field(x.Why))
	}
	fmt.Fprintf(stdout, "IDLE fleet=%d/%d waiting=%d since=%s roots=%s\n", idle.Working, idle.Width, idle.Waiting, oneline.Field(dashed(idle.Since)), oneline.Field(dashed(idle.Trace)))
	fmt.Fprintf(stdout, "RULES OK judgments=%d acting=%d left=%d off=%d by=%s; the run loop's tick answers the acting ones (run --answer-rules)\n",
		len(answers), acting, left, off, dashed(strings.Join(by, ",")))
	return 0
}
