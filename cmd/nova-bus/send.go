// send.go holds the send verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func (w world) send(c *tool.Call) *tool.Out {
	body := c.Str("body")
	if c.Bool("stdin") {
		raw, err := io.ReadAll(io.LimitReader(c.Stdin, bus.MaxBody+1))
		if err != nil {
			return tool.Refuse("--stdin: " + err.Error())
		}
		if len(raw) > bus.MaxBody {
			return tool.Refuse(fmt.Sprintf("the body on stdin is over 1 MiB; at most %d bytes", bus.MaxBody))
		}
		body = string(raw)
	}
	gated := w.requirePush(c)
	b, login, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	draft := bus.Message{
		From: as, To: names(c.Str("to")), CC: names(c.Str("cc")),
		Subject: c.Str("subject"), Re: c.Str("re"), Kind: c.Str("kind"), Body: body, Token: c.Str("token"),
	}
	b.TokenLife, b.TokenCleanup = c.Dur("token-life"), c.Dur("token-cleanup")
	send := b.Send
	if gated {
		// --require-push: the gate, after the message's own problems are named and
		// before anything is written
		send = func(ctx context.Context, m bus.Message) (bus.Message, error) {
			m, err := b.Check(ctx, m)
			if err != nil {
				return m, err
			}
			if err := b.Heard(ctx, slices.Concat([]string{m.From}, m.To, m.CC)...); err != nil {
				return m, err
			}
			return b.Send(ctx, m)
		}
	}
	if c.DryRun() {
		// the message as it would be sent, with no id: nothing is written, and the
		// push gate the write would meet is asked for by name
		send = func(ctx context.Context, m bus.Message) (bus.Message, error) {
			m, err := b.Check(ctx, m)
			if err != nil || !gated {
				return m, err
			}
			return m, b.Heard(ctx, slices.Concat([]string{m.From}, m.To, m.CC)...)
		}
	}
	m, err := send(context.Background(), draft)
	if err != nil {
		return answer(err)
	}
	// the push proof is advice, never a gate: the message landed (or would), and a
	// name nothing proven is pushing into is one NOTE each (SPEC-BUS.md,
	// bus-requires-inbox-push-proof); a gated send proved every name already
	var unheard []string
	if !gated {
		names := slices.Concat([]string{m.From}, m.To, m.CC)
		if m.At.IsZero() {
			// --dry-run: no message was stamped, so judge at the store's now
			unheard, err = b.Unheard(context.Background(), names...)
		} else {
			unheard, err = b.UnheardAt(context.Background(), m.At, names...)
		}
		if err != nil {
			return answer(err)
		}
	}
	sum := sha256.Sum256([]byte(m.Body))
	o := tool.Done().Fact("id", m.ID).Fact("to", strings.Join(m.To, ",")).Fact("cc", strings.Join(m.CC, ","))
	o = loginFact(kindFact(o, m).Fact("at", m.At.Format(time.RFC3339)).
		Fact("bytes", len(m.Body)).Fact("sha256", hex.EncodeToString(sum[:])), login)
	for _, line := range unheard {
		o.Note(line)
	}
	return o
}
