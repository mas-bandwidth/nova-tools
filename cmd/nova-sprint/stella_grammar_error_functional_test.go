//go:build functional

package main

import (
	"bytes"
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"strings"
	"testing"
)

func TestStellaNumericPRLookupPreservesStoreFailure(t *testing.T) {
	t.Parallel()
	_, c := loadedStore(t)
	ctx := context.Background()
	const id = "error-audit~1"
	if err := c.Set(ctx, taskcard.Key(id), "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runCardMove(ctx, "end", []string{"--redis", c.Options().Addr, "--ids", id, "--ok", "--pr", "7", "--head", strings.Repeat("a", 40)}, &out, &errOut)
	if code == 0 || !strings.Contains(out.String()+errOut.String(), "WRONGTYPE") || !strings.Contains(out.String()+errOut.String(), id) {
		t.Fatalf("repo lookup misreported as PR input error: exit=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}
