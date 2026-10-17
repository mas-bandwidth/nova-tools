package friend

import (
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPongRefusesLegacy pins the retired legacy reader: a value in the legacy
// line form is refused by name as a legacy form after the writers switched,
// never read as a pong, and tallied as that refusal.
func TestPongRefusesLegacy(t *testing.T) {
	t.Parallel()

	at := time.Unix(1000, 0).UTC()
	body := PongLine("abc123", 5, 3, 8)
	r := NewPongReader()

	got, form, err := r.Read("bob", at, []byte(body))
	require.Error(t, err, "the legacy form is refused")
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "the refusal is typed: %v", err)
	assert.Equal(t, pongLegacyReason, ref.Reason, "the refusal names the legacy form")
	assert.Equal(t, "PONG REFUSED: legacy; run: "+PongHelp, err.Error())
	assert.Equal(t, Pong{}, got, "a refused legacy value is never read")
	assert.Equal(t, schemaread.FormLegacy, form, "the value is recognised as the legacy form")

	m := bus.Message{From: "bob", To: []string{"ada"}, Subject: PongSubject, Body: body + "\n", At: at}
	got, _, err = r.Message(m)
	require.Error(t, err, "the bus message carrying a legacy value is refused")
	require.True(t, errors.As(err, &ref), "the refusal is typed: %v", err)
	assert.Equal(t, pongLegacyReason, ref.Reason)
	assert.Equal(t, Pong{}, got)

	c := r.Counts()
	assert.Equal(t, uint64(2), c.Reads(schemaread.FormLegacy), "each legacy value is seen as the legacy form")
	assert.Equal(t, uint64(2), c.Refused(pongLegacyReason), "each refusal is tallied by name")
}
