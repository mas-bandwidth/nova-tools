package taskcard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// The cut ledger (nova-tools#4340, the cold read of #4358): card cut --from
// files one issue per row, and a rerun of the same file must file none of
// them twice. The ledger is one hash per file, keyed by the sha256 of the
// file's bytes, never expired:
//
//	cut:<sha256>   repo -> <owner/name>   <row n> -> <issue n>
//
// A row is written the moment its issue is filed (before the next filing),
// and the whole hash is read at the start of a cut, so a rerun after a
// partial or a full filing takes each filed row's issue from the ledger.

// CutLedgerKey is the ledger of the file whose bytes are text.
func CutLedgerKey(text []byte) string {
	sum := sha256.Sum256(text)
	return "cut:" + hex.EncodeToString(sum[:])
}

// CutLedger is one file's filings: the repo they were filed on and each
// row's issue number.
type CutLedger struct {
	Repo   string
	Issues map[int]int
}

// ParseCutLedger reads the hash's fields; a field that is neither repo nor
// a row number with an issue number is an error naming it.
func ParseCutLedger(key string, h map[string]string) (CutLedger, error) {
	l := CutLedger{Issues: map[int]int{}}
	fields := make([]string, 0, len(h))
	for f := range h {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		v := h[f]
		if f == "repo" {
			l.Repo = v
			continue
		}
		row, err1 := strconv.Atoi(f)
		n, err2 := strconv.Atoi(v)
		if err1 != nil || err2 != nil || row < 1 || n < 1 {
			return CutLedger{}, fmt.Errorf("%s field %q = %q is not <row n> -> <issue n>", key, f, v)
		}
		l.Issues[row] = n
	}
	if len(l.Issues) > 0 && l.Repo == "" {
		return CutLedger{}, fmt.Errorf("%s holds rows but no repo", key)
	}
	return l, nil
}

// ReadCutLedger is the ledger at key, empty when the file was never cut.
func ReadCutLedger(ctx context.Context, c redis.Cmdable, key string) (CutLedger, error) {
	pipe := c.Pipeline()
	cmd := pipe.HGetAll(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return CutLedger{}, fmt.Errorf("read %s: %w", key, err)
	}
	return ParseCutLedger(key, cmd.Val())
}

// WriteCutLedger records that row's issue is n on repo, in one round trip.
func WriteCutLedger(ctx context.Context, c redis.Cmdable, key, repo string, row, n int) error {
	pipe := c.Pipeline()
	pipe.HSet(ctx, key, "repo", repo, strconv.Itoa(row), strconv.Itoa(n))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	return nil
}
