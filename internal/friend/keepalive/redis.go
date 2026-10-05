package keepalive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"
)

// Redis implements only the dedicated keepalive lane. The supplied client is
// opened by redisconn; normal bus streams and consumer groups are never used.
type Redis struct{ C *redis.Client }

// Stream names the short-lived recipient lane, distinct from bus.StreamOf.
func Stream(recipient string) (string, error) {
	if why := bus.CheckName(recipient); why != "" {
		return "", fmt.Errorf("keepalive recipient: %s", why)
	}
	return "bus2:keepalive:" + recipient, nil
}

// The stream's own generated ID supplies the retention clock; a client clock
// cannot trim another host's frames early. Exact MINID trimming retains the
// last minute of stream time, and an idle lane expires after a minute.
// Each frame costs one XADD. EVALs are pipelined, with no script-cache retry.
const appendScript = `local id = redis.call('XADD', KEYS[1], '*', 'frame', ARGV[1])
local ms = tonumber(string.match(id, '^(%d+)'))
if ms >= 60000 then
  redis.call('XTRIM', KEYS[1], 'MINID', string.format('%.0f-0', ms - 60000))
end
redis.call('PEXPIRE', KEYS[1], 60000)
return id`

// Head establishes an explicit cursor before the first challenge is sent.
// Using XREAD '$' after a send could skip a reply that has already arrived.
func (r Redis) Head(ctx context.Context, recipient string) (string, error) {
	key, err := Stream(recipient)
	if err != nil {
		return "", err
	}
	rows, err := r.C.XRevRangeN(ctx, key, "+", "-", 1).Result()
	if errors.Is(err, redis.Nil) {
		return "0-0", nil
	}
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "0-0", nil
	}
	return rows[0].ID, nil
}

// AppendBatch validates the whole batch before writing, then pipelines one
// atomic append/trim/expiry per recipient. A pipeline error can follow partial
// writes: successful IDs are retained and other outcomes are marked unknown.
func (r Redis) AppendBatch(ctx context.Context, frames []Frame) ([]AppendResult, error) {
	if len(frames) > BatchLimit {
		return nil, fmt.Errorf("keepalive batch wants at most %d frames", BatchLimit)
	}
	if len(frames) == 0 {
		return nil, nil
	}
	bodies := make([][]byte, len(frames))
	keys := make([]string, len(frames))
	for i, f := range frames {
		if err := f.Validate(); err != nil {
			return nil, fmt.Errorf("keepalive frame %d: %w", i, err)
		}
		key, err := Stream(f.To)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(f)
		if err != nil {
			return nil, err
		}
		if len(body) > FrameLimit {
			return nil, fmt.Errorf("keepalive frame %d exceeds %d bytes", i, FrameLimit)
		}
		keys[i], bodies[i] = key, body
	}
	pipe := r.C.Pipeline()
	cmds := make([]*redis.Cmd, len(frames))
	for i := range frames {
		cmds[i] = pipe.Eval(ctx, appendScript, []string{keys[i]}, string(bodies[i]))
	}
	err := redisconn.Exec(ctx, pipe)
	results := make([]AppendResult, len(frames))
	for i, cmd := range cmds {
		id, resultErr := cmd.Text()
		if resultErr != nil || !streamID(id) {
			results[i].Unknown = true
			if err == nil {
				if resultErr != nil {
					err = resultErr
				} else {
					err = fmt.Errorf("keepalive append %d returned no valid stream ID", i)
				}
			}
			continue
		}
		results[i].ID = id
	}
	return results, err
}

// ReadBatch advances over every raw entry, including malformed ones, so a bad
// frame cannot wedge the lane. Count bounds records; FrameLimit bounds frames
// produced by this writer, not arbitrary data written directly by others.
func (r Redis) ReadBatch(ctx context.Context, recipient, after string, limit int) (Read, error) {
	if after == "" {
		after = "0-0"
	}
	out := Read{Next: after}
	key, err := Stream(recipient)
	if err != nil {
		return out, err
	}
	if !streamID(after) {
		return out, fmt.Errorf("keepalive cursor wants a numeric Redis stream ID")
	}
	if limit < 1 || limit > ReadLimit {
		return out, fmt.Errorf("keepalive read limit wants 1..%d", ReadLimit)
	}
	rows, err := r.C.XRangeN(ctx, key, "("+after, "+", int64(limit)).Result()
	if errors.Is(err, redis.Nil) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		entry := Entry{ID: row.ID}
		body, ok := row.Values["frame"].(string)
		if !ok || len(body) > FrameLimit {
			entry.DecodeError = "missing or oversized keepalive frame"
		} else {
			entry.Frame, entry.DecodeError = decodeFrame(body)
			if entry.DecodeError == "" && entry.Frame.To != recipient {
				entry.DecodeError = "keepalive frame names another recipient"
			}
		}
		out.Entries = append(out.Entries, entry)
		out.Next = row.ID
	}
	return out, nil
}

func decodeFrame(body string) (Frame, string) {
	var f Frame
	d := json.NewDecoder(bytes.NewBufferString(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return Frame{}, "invalid keepalive frame JSON"
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return Frame{}, "keepalive frame has trailing data"
	}
	if err := f.Validate(); err != nil {
		return Frame{}, "invalid keepalive frame fields"
	}
	return f, ""
}

func streamID(id string) bool {
	if len(id) > 41 {
		return false
	}
	a, b, ok := strings.Cut(id, "-")
	if !ok {
		return false
	}
	_, aerr := strconv.ParseUint(a, 10, 64)
	_, berr := strconv.ParseUint(b, 10, 64)
	return aerr == nil && berr == nil
}
