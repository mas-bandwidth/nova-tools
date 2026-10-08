// receipt.go holds the receipt verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// receipts is as's receipts: the named ids', or every one held (SPEC-BUS.md,
// message-receipts).
func (w world) receipts(c *tool.Call) *tool.Out {
	b, login, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	got, now, err := b.Stages(context.Background(), as, names(c.Str("id"))...)
	if err != nil {
		return answer(err)
	}
	o := tool.Done().Fact("count", len(got))
	for _, s := range got {
		state, age := s.State, "-"
		if state == "" {
			state = "none"
		} else {
			age = s.Age(now).Round(time.Second).String()
		}
		o.Item("receipt", "id", s.ID, "state", state, "age", age)
	}
	return loginFact(o, login)
}
