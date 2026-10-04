package update

// Delivery over the Redis bus (SPEC-UPDATE rules 24, 25 and 27; SPEC-BUS.md):
// a note is one message from --as to --to, sent in-process through
// internal/bus, with no child process and no checkout. The store is reached
// the way nova-bus reaches it (loopback or the tailnet only, the fleet login
// from the environment, the password never on the line).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// replayWindow is how far before a pending note's save the log is read to find
// the note again: it covers the clock difference between this host and the
// store, and the note is matched by its bytes, so a wider window can only find
// the same message.
const replayWindow = time.Hour

// openBus is the store at addr: the Environment's seam in a test, else the
// Redis the fleet's login dials (SPEC-UPDATE rule 24).
func (env Environment) openBus(ctx context.Context, addr string) (bus.Store, func(), error) {
	if env.Bus != nil {
		return env.Bus(ctx, addr)
	}
	if why := bus.CheckAddr(ctx, addr, func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}); why != "" {
		return nil, nil, errors.New(why)
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		return nil, nil, err
	}
	return bus.Redis{C: st.Client()}, func() { _ = st.Close() }, nil // ignored: the note is sent or refused before the close, which has nothing to add
}

// recipients is the --to list as names.
func recipients(to string) []string {
	var out []string
	for _, w := range strings.Split(to, ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// busSaid is the bus's refusal or the store's failure as one clipped line: the
// message's own problems for a refusal (SEND REFUSED), the connection's words
// for anything else (SEND FAIL). Neither carries a note body or a secret.
func busSaid(err error) string {
	var refusal *bus.Refusal
	if errors.As(err, &refusal) {
		return "SEND REFUSED: " + clip(oneline.Err(refusal), 200)
	}
	return "SEND FAIL " + clip(oneline.Err(err), 200)
}

// sendLine is the SEND OK line a confirmed note leaves in the receipt: the
// message's id and recipients, the body's length and digest as the bus holds
// them, and state=sent, or already-published for a note an earlier run landed.
func sendLine(m bus.Message, state string) string {
	sum := sha256.Sum256([]byte(m.Body))
	return fmt.Sprintf("SEND OK id=%s to=%s at=%s bytes=%d sha256=%s state=%s", m.ID, strings.Join(m.To, ","), m.At.UTC().Format(time.RFC3339), len(m.Body), hex.EncodeToString(sum[:]), state)
}

// sendNote sends the message m on the bus at o.redis, within ctx. With
// replayed, an earlier run may have sent it and lost the answer, so the log is
// read first and a message from the same sender with the same subject, body
// and recipients is the confirmation: a note is never sent twice (rule 25).
// It answers the SEND OK line and the message's id.
func sendNote(ctx context.Context, env Environment, o options, m bus.Message, saved time.Time, replayed bool) (line, id string, err error) {
	st, closeStore, err := env.openBus(ctx, o.redis)
	if err != nil {
		return "", "", fmt.Errorf("SEND FAIL %s", clip(oneline.Err(err), 200))
	}
	if closeStore != nil {
		defer closeStore()
	}
	b := &bus.Bus{Store: st}
	m.To = slices.Sorted(slices.Values(m.To))
	if replayed {
		entries, lerr := b.Log(ctx, bus.IDAt(saved.Add(-replayWindow)))
		if lerr != nil {
			return "", "", errors.New(busSaid(lerr))
		}
		for _, e := range entries {
			if f := e.Message(); f.From == m.From && f.Subject == m.Subject && f.Body == m.Body && slices.Equal(f.To, m.To) {
				return sendLine(f, "already-published"), f.ID, nil
			}
		}
	}
	sent, err := b.Send(ctx, m)
	if err != nil {
		return "", "", errors.New(busSaid(err))
	}
	return sendLine(sent, "sent"), sent.ID, nil
}
