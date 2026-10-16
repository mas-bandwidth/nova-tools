package main

import (
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// sprintSent recognizes the sprint server's coordinator notices when it sends
// under the coordinator's own name (docs/SPEC-HOOK.md, "The hook shows what the
// server says under its own name"). Other messages to self stay suppressed.
func sprintSent(m bus.Message, as string) bool {
	if m.From != as {
		return false
	}
	switch m.Kind {
	case bus.KindRequest:
		if m.Subject == "nova-sprint: judgments waiting on the coordinator" {
			return true
		}
		count, ok := strings.CutPrefix(m.Subject, "nova-sprint: ")
		if !ok {
			return false
		}
		count, ok = strings.CutSuffix(count, " new for the coordinator")
		if !ok {
			return false
		}
		n, err := strconv.Atoi(count)
		return err == nil && n > 0 && strconv.Itoa(n) == count
	case bus.KindBlocker:
		return m.Subject == "nova-sprint: decisions owed"
	}
	return false
}
