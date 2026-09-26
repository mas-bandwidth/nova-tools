package ws_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// legacy writes the friend-queue shape TK.adopt reads: task:<id> hashes
// with owner and a friend-queue state, the stream in a `stream` field (even
// ids) or a "STREAM: <s> |" title (odd ids), every fifth id with neither, the
// owner's idx sets for sprint S, and q:waiting / q:blocked.
func legacy(t *testing.T, c *redis.Client, n int) (ids []string, want map[string]string) {
	t.Helper()
	ctx := context.Background()
	pipe := c.Pipeline()
	want = map[string]string{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("L%04d", i)
		owner := fmt.Sprintf("f%d", i%4)
		stream := wstest.StreamName(i % 10)
		// created_at as friend-queue wrote it (RFC 3339 UTC), as epoch ms, or
		// absent (migrate writes the score it chose back as created_at)
		fields := []any{"owner", owner}
		switch {
		case i%7 == 0:
			fields = append(fields, "created_at", time.Unix(1790000000+int64(i), 0).UTC().Format(time.RFC3339))
		case i%11 == 0:
		default:
			fields = append(fields, "created_at", fmt.Sprint(5000+i))
		}
		switch {
		case i%5 == 4:
			fields = append(fields, "title", "no stream here")
		case i%2 == 0:
			fields = append(fields, "stream", stream, "title", "plain")
		default:
			fields = append(fields, "title", "STREAM: "+stream+" | the work")
		}
		ix := "sprint:S:idx:" + owner + ":"
		var state, wsState string
		switch i % 6 {
		case 0:
			state, wsState = "open", "ready"
			pipe.SAdd(ctx, ix+"open", id)
		case 1:
			state, wsState = "working", "working"
			pipe.SAdd(ctx, ix+"working", id)
			fields = append(fields, "leased_at", time.Now().UTC().Format(time.RFC3339))
		case 2:
			state, wsState = "closed", "done"
			pipe.SAdd(ctx, ix+"closed", id)
		case 3:
			state, wsState = "waiting", "waiting"
			pipe.ZAdd(ctx, "q:waiting", redis.Z{Score: float64(9000 + i), Member: id})
		case 4:
			state, wsState = "blocked", "waiting"
			pipe.ZAdd(ctx, "q:blocked", redis.Z{Score: 1, Member: id})
		case 5:
			// the index says working though the hash still says open (a take
			// that crashed between SMOVE and HSET): the index wins
			state, wsState = "open", "working"
			pipe.SAdd(ctx, ix+"working", id)
			fields = append(fields, "leased_at", time.Now().UTC().Format(time.RFC3339))
		}
		fields = append(fields, "state", state)
		pipe.HSet(ctx, "task:"+id, fields...)
		ids = append(ids, id)
		if i%5 != 4 {
			want[id] = wsState
		}
	}
	pipe.Set(ctx, "task:notahash", "x", 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return ids, want
}

func TestReadIDsAndParseStreams(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ids")
	if err := os.WriteFile(path, []byte("a b\n# comment\nc,a  # trailing\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ids, err := ws.ReadIDs("@"+path, nil)
	if err != nil || strings.Join(ids, " ") != "a b c" {
		t.Fatalf("ReadIDs file %v %v", ids, err)
	}
	ids, err = ws.ReadIDs("@-", strings.NewReader("x\ny\n"))
	if err != nil || strings.Join(ids, " ") != "x y" {
		t.Fatalf("ReadIDs stdin %v %v", ids, err)
	}
	ids, err = ws.ReadIDs("p,q", nil)
	if err != nil || strings.Join(ids, " ") != "p q" {
		t.Fatalf("ReadIDs list %v %v", ids, err)
	}
	if _, err := ws.ReadIDs("@"+path+".missing", nil); err == nil {
		t.Fatal("a missing ids file read")
	}
	if got := ws.ParseStreams(" swarm: cards | nova-sprint ||"); strings.Join(got, "/") != "swarm: cards/nova-sprint" {
		t.Fatalf("ParseStreams %q", got)
	}
}
