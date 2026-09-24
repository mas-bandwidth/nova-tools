package brief

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// briefRedis starts the throwaway Redis every brief test shares (testutil.Start:
// skip on a laptop without redis-server, fail under NOVA_CI=1) and loads the
// nova_sprint library, so ns_task_brief is the one writer of the brief fields.
func briefRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

// runLint runs Lint and returns its exit code and the BRIEF REFUSED lines.
func runLint(t *testing.T, st *store.Store, path, taskFlag string) (int, []string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Lint(context.Background(), st, path, taskFlag, &out, &errb)
	return code, refusedLines(errb.String())
}

func refusedLines(s string) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "BRIEF REFUSED ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// linesWithField returns the refusal lines naming field=<f>.
func linesWithField(lines []string, f string) []string {
	var got []string
	for _, l := range lines {
		if strings.Contains(l, " field="+f+" ") || strings.HasSuffix(l, " field="+f) {
			got = append(got, l)
		}
	}
	return got
}
