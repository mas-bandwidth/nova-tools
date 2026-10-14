package main

import (
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// friendsShown bounds the text listing; --json lists every name.
const friendsShown = 50

// friends is the verb: every name's state from the hook record alone, by the
// store's TIME, and the coordinator and the heartbeat server's age.
func (w world) friends(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, false)
	if refused != nil {
		return refused
	}
	defer s.close()
	ctx, cancel := call()
	defer cancel()
	names, err := s.hooks.Names(ctx)
	if err != nil {
		return tool.Refuse("the hook names could not be read: " + err.Error())
	}
	sts, _, err := s.hooks.Read(ctx, names...)
	if err != nil {
		return tool.Refuse("the hook records could not be read: " + err.Error())
	}
	sv, err := s.hooks.Serve(ctx)
	if err != nil {
		return tool.Refuse("the heartbeat server's stamp could not be read: " + err.Error())
	}
	o := tool.Done()
	coord, up := "", 0
	for _, st := range sts {
		state, since, reason := "down", st.Unhooked, st.Reason
		if st.State == hook.StateProven {
			state, since, reason = "up", st.Armed, ""
			up++
			if st.Role == hook.Coordinator && coord == "" {
				coord = st.Name
			}
		} else if reason == "" {
			reason = st.State
		}
		o.Item("name", "name", st.Name, "role", string(st.Role), "state", state, "since", stamp(since), "reason", tool.Text(reason),
			"last_challenge_at", stamp(st.BeatAt), "last_answer_at", stamp(st.AnswerAt), "misses", st.Misses)
	}
	o.Fact("count", len(sts)).Fact("up", up).Fact("coordinator", coord)
	alive, age := false, ""
	if sv.OK {
		alive, age = sv.Now.Sub(sv.At) <= 2*sv.Every, sv.Now.Sub(sv.At).Round(time.Second).String()
	}
	o.Fact("serve_alive", alive).Fact("serve_age", age)
	if !c.Given("json") {
		o.Cap(friendsShown)
		for i := range o.More {
			o.More[i].Remedy = "nova-hook friends --json"
		}
	}
	return o
}

// stamp is t as RFC 3339 in UTC, "" when never.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
