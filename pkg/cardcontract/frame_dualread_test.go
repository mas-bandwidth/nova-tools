package cardcontract_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestCardFrameDualRead is the reader half of the card contract frame's table
// adoption (v1.5 aspect 13; the owner's rules S2 and S4): the frame ReadFrame
// reads is read alike from its table wire, the padded standard base64 of it,
// and the legacy JSON form WriteFrame writes today; a malformed value of each
// form is refused by name (`<RECORD> REFUSED: <reason>; run: <help>`) and
// never read as absent, empty or zero; a clamped value is refused; and each
// read is counted once under its form. Files are read from a t.TempDir() the
// test writes.
func TestCardFrameDualRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := cardcontract.Frame{
		Kind: "work", Card: "a13-card-frame-dual-read", Attempt: 2, Tier: "pro",
		Model: "deepseek/deepseek-r1", Repo: "https://example.com/Example-Owner/example-repo.git",
		BaseRef: "sprint/v15-schema-tables", StageSha: "0123456789abcdef0123456789abcdef01234567",
		Branch: "sprint/a13-card-frame-dual-read.w1.g1.e21", PrevHead: "0123456789abcdef0123456789abcdef01234567",
		PrevFrom: 1, Why: "attempt 1", Finding: "f.go:12", Fix: "fix it", ReviewBase: "main",
		Stage: []string{"schema/recipe-1.md", "schema/recipe-2.md"}, Recipes: "/work/recipes",
		DecideBounce: "0.9", DecideReview: "review", DecideGateFlaky: "no", DecideGatePreexisting: "no",
	}
	wire := frameImage(t, want)
	base64Text := []byte(base64.StdEncoding.EncodeToString(wire))
	legacyPath := filepath.Join(dir, "legacy.json")
	require.NoError(t, cardcontract.WriteFrame(legacyPath, want))
	legacyBytes := readFile(t, legacyPath)

	// the reader is shared by the package's tests, so this test pins the
	// counts it earns on top of what it found, not an absolute total
	counts := cardcontract.FrameReadCounts()
	bin0 := counts.Reads(schemaread.FormBinary)
	b640 := counts.Reads(schemaread.FormBase64)
	leg0 := counts.Reads(schemaread.FormLegacy)

	for _, c := range []struct {
		name string
		body []byte
	}{
		{"binary", wire},
		{"base64", base64Text},
		{"legacy", legacyBytes},
	} {
		p := filepath.Join(dir, c.name)
		require.NoError(t, os.WriteFile(p, c.body, 0o600))
		got, err := cardcontract.ReadFrame(p)
		require.NoError(t, err, c.name)
		assert.Equal(t, want, got, "one value read alike from its %s", c.name)
	}

	// malformed binary and base64 images refuse by name, each read counted
	named := 0
	for _, c := range []struct {
		name string
		body []byte
	}{{"binary", wire}, {"base64", base64Text}} {
		for j, m := range damaged(c.body) {
			dp := filepath.Join(dir, fmt.Sprintf("%s-damaged-%d", c.name, j))
			require.NoError(t, os.WriteFile(dp, m, 0o600))
			_, err := cardcontract.ReadFrame(dp)
			var ref *schemaread.Refusal
			require.ErrorAs(t, err, &ref, "%s damaged %d", c.name, j)
			assert.Equal(t, "FRAME", ref.Name, c.name)
			named++
		}
	}

	// malformed legacy frames refuse by name (a prefix cut at a whitespace
	// boundary parses, so the malformed legacy inputs are named, not every cut)
	legacyBad := [][]byte{
		[]byte("{"),
		[]byte(`{"kind":`),
		legacyBytes[:len(legacyBytes)-3],
	}
	for j, m := range legacyBad {
		dp := filepath.Join(dir, fmt.Sprintf("legacy-bad-%d", j))
		require.NoError(t, os.WriteFile(dp, m, 0o600))
		_, err := cardcontract.ReadFrame(dp)
		var ref *schemaread.Refusal
		require.ErrorAs(t, err, &ref, "legacy bad %d", j)
		assert.Equal(t, "FRAME", ref.Name, "legacy")
		named++
	}

	// a clamped value (a kind no word names) is refused
	clamp := want
	clamp.Kind = ""
	clampWire := frameImage(t, clamp)
	cp := filepath.Join(dir, "clamped")
	require.NoError(t, os.WriteFile(cp, clampWire, 0o600))
	_, err := cardcontract.ReadFrame(cp)
	require.Error(t, err, "a clamped kind is refused")

	// the empty cut of damaged has no form and is counted under none
	assert.GreaterOrEqual(t, counts.Reads(schemaread.FormBinary), bin0+uint64(len(damaged(wire))+1), "binary reads counted")
	assert.GreaterOrEqual(t, counts.Reads(schemaread.FormBase64), b640+uint64(len(damaged(base64Text))), "base64 reads counted")
	assert.GreaterOrEqual(t, counts.Reads(schemaread.FormLegacy), leg0+uint64(1+len(legacyBad)), "legacy reads counted")
	assert.Zero(t, counts.Reads(schemaread.FormJSON), "the schema JSON form is not read")
	assert.GreaterOrEqual(t, refusedTotal(counts), uint64(named), "every damaged read is counted as refused, by reason")
}

// frameImage is the Frame table's binary wire for the card frame f.
func frameImage(t *testing.T, f cardcontract.Frame) []byte {
	t.Helper()
	var b decidewire.FrameBuilder
	require.True(t, b.Init())
	defer b.Shutdown()
	fillFrame(&b.Main, b.GetRoot(), f)
	require.True(t, b.Lock())
	root := b.AsConst()
	require.NotNil(t, root)
	n := decidewire.FrameMeasure(root)
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, decidewire.FrameSave(root, buf))
	return buf
}

// fillFrame fills the Frame table's root r from the card frame f.
func fillFrame(w *decidewire.TableWorker, r *decidewire.Frame, f cardcontract.Frame) {
	switch f.Kind {
	case "work":
		r.Kind = decidewire.FrameKindWork
	case "read":
		r.Kind = decidewire.FrameKindRead
	}
	setS(w, &r.Card, f.Card)
	r.Attempt = int64(f.Attempt)
	setS(w, &r.Tier, f.Tier)
	setS(w, &r.Model, f.Model)
	setS(w, &r.Repo, f.Repo)
	setS(w, &r.BaseRef, f.BaseRef)
	setS(w, &r.StageSha, f.StageSha)
	setS(w, &r.Branch, f.Branch)
	setS(w, &r.PrevHead, f.PrevHead)
	r.PrevAttempt = int64(f.PrevFrom)
	setS(w, &r.Why, f.Why)
	setS(w, &r.Finding, f.Finding)
	setS(w, &r.Fix, f.Fix)
	setS(w, &r.ReviewBase, f.ReviewBase)
	for _, p := range f.Stage {
		setS(w, &decidewire.FrameStageAdd(w, &r.Stage).Path, p)
	}
	setS(w, &r.Recipes, f.Recipes)
	setS(w, &r.DecideBounce, f.DecideBounce)
	setS(w, &r.DecideReview, f.DecideReview)
	setS(w, &r.DecideGateFlaky, f.DecideGateFlaky)
	setS(w, &r.DecideGatePreexisting, f.DecideGatePreexisting)
}

func setS(w *decidewire.TableWorker, slot *decidewire.TableRef, text string) {
	if text != "" {
		decidewire.TableStringEmplace(w, slot, []byte(text))
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func refusedTotal(c *schemaread.Counts) uint64 {
	total := uint64(0)
	for k, n := range c.Snapshot() {
		if len(k) > 8 && k[:8] == "refused_" {
			total += n
		}
	}
	return total
}

// damaged is in cut at each length (every length for a short value, the first
// and last 256 and a stride between for a long one) and in with bytes after
// its end.
func damaged(in []byte) [][]byte {
	var cuts []int
	if len(in) <= 768 {
		for n := 0; n < len(in); n++ {
			cuts = append(cuts, n)
		}
	} else {
		for n := 0; n < 256; n++ {
			cuts = append(cuts, n, len(in)-256+n)
		}
		for n := 256; n < len(in)-256; n += len(in)/97 + 1 {
			cuts = append(cuts, n)
		}
	}
	out := make([][]byte, 0, len(cuts)+3)
	for _, n := range cuts {
		out = append(out, bytes.Clone(in[:n]))
	}
	for _, tail := range []string{"\x00", "x", "AAAA"} {
		out = append(out, append(bytes.Clone(in), tail...))
	}
	return out
}
