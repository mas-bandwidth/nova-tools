package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunNamesTheCurrentHolderWhenItRefusesAnOldReport(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	dir := t.TempDir()
	outbox := filepath.Join(dir, "outbox", "taken.w1~15")
	require.NoError(t, os.MkdirAll(outbox, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: LAND\nHead: 0123456789abcdef0123456789abcdef01234567\n"), 0o644))
	w := r.world()
	w.cards = func(context.Context, string, []string) (string, error) { return `{"friend":"bob","cards":[]}`, nil }
	looked := 0
	w.holders = func(context.Context, string) (map[string]string, error) {
		looked++
		return map[string]string{"taken.w1": "cy"}, nil
	}
	finished := 0
	w.finish = func(context.Context, string, []string) error { finished++; return nil }
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	beats := 0
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		if beats == 3 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=1", nil
	}
	var out, errb strings.Builder
	code := run([]string{"run", "--server", "127.0.0.1:6390", "--as", "bob", "--harness", "opencode", "--session", "ses_main", "--dir", dir, "--coordinator", "ada"}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	assert.Positive(t, looked, "the actual holder source is wired into the daemon")
	assert.Zero(t, finished, "no finish is sent for a card off her row")
	assert.Contains(t, out.String(), "refused: card taken.w1 is not on her row, no longer hers; cy holds it now")
}

func TestHolderViewRejectsMalformedOrUnrelatedDocuments(t *testing.T) {
	t.Parallel()
	holders, err := parseHolders(`{"view":"cards","schema":1,"cards":[{"id":"a.w1","holder":"cy"},{"id":"b.w1"}]}`)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a.w1": "cy"}, holders)
	for _, raw := range []string{
		`broken`, `{"view":"worker","schema":1}`, `{"view":"cards","schema":2}`,
		`{"view":"cards","schema":1,"cards":[{"holder":"cy"}]}`,
		`{"view":"cards","schema":1,"cards":[{"id":"a"},{"id":"a","holder":"cy"}]}`,
	} {
		_, err := parseHolders(raw)
		assert.Error(t, err, raw)
	}
}

func TestMalformedHolderViewNeverIncludesItsContentInAnError(t *testing.T) {
	t.Parallel()
	secret := "test-holder-secret-abc123"
	for _, raw := range []string{
		`{"view":"` + secret + `","schema":1}`,
		`{"view":"cards","schema":1,"cards":[{"id":"` + secret + `"},{"id":"` + secret + `"}]}`,
	} {
		_, err := parseHolders(raw)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), secret)
	}
}
