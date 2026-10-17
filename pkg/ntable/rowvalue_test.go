package ntable

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/require"
)

func TestRowValueSeam(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"AAECAw==", ` {"schema":"value","text":"<>&"} `, `{"legacy":true}`, "plain\ntext", ""} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			body, err := payload(BatchManifest{Members: []BatchMemberEntry{{ID: "m", Set: map[string]string{"value": value}}}})
			require.NoError(t, err)
			var manifest BatchManifest
			require.NoError(t, json.Unmarshal([]byte(body), &manifest))
			require.Equal(t, value, manifest.Members[0].Set["value"])
			members, err := flatMembers([]any{value, "1"})
			require.NoError(t, err)
			require.Equal(t, value, members[0].Member)
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			var delta BatchDelta
			require.NoError(t, json.Unmarshal([]byte(`{"members":[{"id":"m","fields_set":{"value":`+string(encoded)+`},"fields_unset":["other"],"fields":{"value":{"before":`+string(encoded)+`,"after":`+string(encoded)+`}}}]}`), &delta))
			require.Equal(t, value, delta.Members[0].FieldsSet["value"])
			require.Equal(t, value, *delta.Members[0].Fields["value"].Before)
			require.Equal(t, value, *delta.Members[0].Fields["value"].After)
			require.Equal(t, []string{"other"}, delta.Members[0].FieldsUnset)
			wire := []any{"RECEIPT", "op-1", "0", "0", "1", "applied", string(mustRowDelta(t, delta))}
			client := newCoverCmdable().answer(FnApply, []any{"OK", wire})
			manifest.Table = "demo"
			manifest.Schema = 1
			manifest.Epoch = "0"
			manifest.ExpectedTableRevision = "0"
			manifest.OperationID = "op-1"
			manifest.Members[0].Expect = &MemberExpect{}
			receipt, err := ApplyBatch(context.Background(), client, manifest)
			require.NoError(t, err)
			require.Equal(t, value, receipt.BatchDelta.Members[0].FieldsSet["value"])

		})
	}
	for _, row := range []struct {
		text   string
		legacy bool
		form   schemaread.Form
	}{
		{"AAECAw==", false, schemaread.FormBase64},
		{`{"schema":"value"}`, false, schemaread.FormJSON},
		{`{"legacy":true}`, true, schemaread.FormLegacy},
	} {
		form, err := RowValueForm(row.text, row.legacy)
		require.NoError(t, err)
		require.Equal(t, row.form, form)
	}

	_, err := payload(BatchManifest{Members: []BatchMemberEntry{{ID: "m", Set: map[string]string{"value": string([]byte{0xff})}}}})
	require.ErrorContains(t, err, "invalid_utf8")
}

func mustRowDelta(t *testing.T, delta BatchDelta) []byte {
	t.Helper()
	data, err := json.Marshal(delta)
	require.NoError(t, err)
	return data
}
