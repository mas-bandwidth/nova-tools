package cardcontract_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	wire "github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The frame ReadFrame reads is read alike from the binary image of the decide
// Frame table, the padded standard base64 of that image, the table's schema
// JSON text and the legacy JSON WriteFrame writes today (S2), refused by name
// when damaged and counted per form (C13). Every writer still writes the
// legacy form.
func TestCardFrameDualRead(t *testing.T) {
	t.Parallel()
	rec := cardcontract.Frame{
		Kind: "work", Card: "c1", Attempt: 2,
		Repo: "/srv/nova-tools", StageSha: "0123456789abcdef0123456789abcdef01234567",
		Branch: "sprint/v15-schema-tables", Tier: "pro", Model: "claude",
		Stage: []string{"pr/4926.md", "recipes/one.md"},
	}
	forms := formsOf(t, rec)

	for _, f := range forms.each() {
		got, err := cardcontract.FrameReadBytes(f.in)
		if !assert.NoError(t, err, "%s form reads", f.form) {
			continue
		}
		assert.Equal(t, rec, got, "%s form reads the frame equal", f.form)
	}

	// A truncated, flipped and clamped value of every form is refused by name,
	// never read as absent, empty or zero (S2, C13).
	refused := 0
	for _, f := range forms.each() {
		for _, bad := range damageOf(t, f.form, f.in) {
			_, err := cardcontract.FrameReadBytes(bad)
			assert.True(t, frameRefusedByName(err), "%s damaged value refused by name: %v", f.form, err)
			refused++
		}
	}
	assert.Positive(t, refused)

	// Reads are counted per record per form (C13).
	var total uint64
	for _, f := range forms.each() {
		n := cardcontract.FrameReadCounts().Reads(f.form)
		assert.NotZero(t, n, "%s form counted", f.form)
		total += n
	}
	assert.Positive(t, total)
}

// frameTableModel is the schema JSON text shape of the Frame table.
type frameTableModel struct {
	Kind        *string          `json:"kind,omitempty"`
	Card        *string          `json:"card,omitempty"`
	Attempt     *int64           `json:"attempt,omitempty"`
	Tier        *string          `json:"tier,omitempty"`
	Model       *string          `json:"model,omitempty"`
	Repo        *string          `json:"repo,omitempty"`
	BaseRef     *string          `json:"base_ref,omitempty"`
	StageSha    *string          `json:"stage_sha,omitempty"`
	Branch      *string          `json:"branch,omitempty"`
	PrevHead    *string          `json:"prev_head,omitempty"`
	PrevAttempt *int64           `json:"prev_attempt,omitempty"`
	Why         *string          `json:"why,omitempty"`
	Finding     *string          `json:"finding,omitempty"`
	Fix         *string          `json:"fix,omitempty"`
	ReviewBase  *string          `json:"review_base,omitempty"`
	Stage       []stagePathModel `json:"stage,omitempty"`
}

type stagePathModel struct {
	Path *string `json:"path,omitempty"`
}

func sp(s string) *string { return &s }
func ip(v int64) *int64   { return &v }

// frameForms is one frame in every form.
type frameForms struct {
	binary, base64, json, legacy []byte
}

func (f frameForms) each() []struct {
	form schemaread.Form
	in   []byte
} {
	return []struct {
		form schemaread.Form
		in   []byte
	}{{schemaread.FormBinary, f.binary}, {schemaread.FormBase64, f.base64}, {schemaread.FormJSON, f.json}, {schemaread.FormLegacy, f.legacy}}
}

func frameImage(t testing.TB, m frameTableModel) (image, text []byte) {
	t.Helper()
	in, err := json.Marshal(m)
	require.NoError(t, err)
	var b wire.FrameBuilder
	var r wire.TableReport
	require.True(t, b.Init() && wire.FrameFromJson(&b, in, &r) && b.Lock(), "schema JSON text %s loads", in)
	v := b.AsConst()
	n := wire.FrameMeasure(v)
	require.Positive(t, n)
	image = make([]byte, n)
	require.Equal(t, n, wire.FrameSave(v, image))
	m2 := wire.FrameToJsonMeasure(v)
	require.Positive(t, m2)
	text = make([]byte, m2)
	require.Equal(t, m2, wire.FrameToJson(v, text))
	return image, text
}

func formsOf(t testing.TB, rec cardcontract.Frame) frameForms {
	t.Helper()
	kind := rec.Kind
	if kind == "work" {
		kind = "Work"
	} else if kind == "read" {
		kind = "Read"
	}
	m := frameTableModel{Kind: sp(kind), Card: sp(rec.Card)}
	if rec.Attempt != 0 {
		m.Attempt = ip(int64(rec.Attempt))
	}
	if rec.PrevFrom != 0 {
		m.PrevAttempt = ip(int64(rec.PrevFrom))
	}
	if rec.Tier != "" {
		m.Tier = sp(rec.Tier)
	}
	if rec.Model != "" {
		m.Model = sp(rec.Model)
	}
	if rec.Repo != "" {
		m.Repo = sp(rec.Repo)
	}
	if rec.BaseRef != "" {
		m.BaseRef = sp(rec.BaseRef)
	}
	if rec.StageSha != "" {
		m.StageSha = sp(rec.StageSha)
	}
	if rec.Branch != "" {
		m.Branch = sp(rec.Branch)
	}
	if rec.PrevHead != "" {
		m.PrevHead = sp(rec.PrevHead)
	}
	if rec.Why != "" {
		m.Why = sp(rec.Why)
	}
	if rec.Finding != "" {
		m.Finding = sp(rec.Finding)
	}
	if rec.Fix != "" {
		m.Fix = sp(rec.Fix)
	}
	if rec.ReviewBase != "" {
		m.ReviewBase = sp(rec.ReviewBase)
	}
	for _, s := range rec.Stage {
		m.Stage = append(m.Stage, stagePathModel{Path: sp(s)})
	}
	image, text := frameImage(t, m)
	legacy, err := json.MarshalIndent(rec, "", "  ")
	require.NoError(t, err)
	legacy = append(legacy, '\n')
	return frameForms{binary: image, base64: []byte(base64.StdEncoding.EncodeToString(image)), json: text, legacy: legacy}
}

// frameRefusedByName is true when err is the named frame refusal.
func frameRefusedByName(err error) bool {
	var r *schemaread.Refusal
	return errors.As(err, &r) && r.Name == "FRAME" && strings.Contains(r.Error(), "FRAME REFUSED: ")
}

// damageOf is a truncated and a flipped-byte value of the form, each refused
// by name. The Frame table bounds no field (schemas/decide/decide.schema), so
// no clamped value is constructible; damage is a wrong form, a truncated image
// or a flipped byte.
func damageOf(t testing.TB, form schemaread.Form, in []byte) [][]byte {
	t.Helper()
	switch form {
	case schemaread.FormBinary:
		flipped := append([]byte(nil), in...)
		flipped[0] ^= 0x01
		return [][]byte{in[:4], flipped}
	case schemaread.FormBase64:
		flipped := append([]byte(nil), in...)
		flipped[0] ^= 0x01
		return [][]byte{[]byte(base64.StdEncoding.EncodeToString(in[:4])), flipped}
	case schemaread.FormJSON:
		// `[` is no form (unknown_form); an unclosed object is legacy damage.
		return [][]byte{[]byte(`[`), []byte(`{"kind":`)}
	default:
		// legacy: an unclosed object and a lone brace are malformed.
		return [][]byte{[]byte(`{"kind":`), []byte(`{`)}
	}
}
