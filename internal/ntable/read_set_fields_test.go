package ntable_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Projection is optional on the existing wire scope; the default remains full
// fields and invalid field requests fail before an exchange (SPEC-NOVA-TABLE).
func TestReadSetFieldProjectionIsOptionalAndValidated(t *testing.T) {
	t.Parallel()
	manyFields := make([]string, ntable.LimitReadSetFields+1)
	for i := range manyFields {
		manyFields[i] = fmt.Sprintf("field-%d", i)
	}
	for _, tc := range []struct {
		name   string
		fields []string
		valid  bool
	}{
		{"default", nil, true}, {"empty", []string{}, true}, {"usage", []string{"usage"}, true},
		{"utf8", []string{"利用"}, true}, {"invalid utf8", []string{"\xff"}, false}, {"blank", []string{""}, false}, {"control", []string{"bad\nname"}, false}, {"duplicate", []string{"usage", "usage"}, false},
		{"epoch", []string{"epoch"}, false}, {"revision", []string{"revision"}, false}, {"place", []string{"place:demo"}, false},
		{"over bound", manyFields, false},
		{"field byte bound", []string{strings.Repeat("f", ntable.LimitReadSetFieldNameBytes)}, true}, {"field bytes over bound", []string{strings.Repeat("f", ntable.LimitReadSetFieldNameBytes+1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scope := ntable.ReadSetScope{Members: []string{"member"}, Fields: tc.fields}
			if !tc.valid {
				_, err := ntable.QueueReadSet(context.Background(), nil, "demo", scope)
				require.Error(t, err)
				if tc.name == "over bound" || tc.name == "field bytes over bound" {
					var limit *ntable.LimitError
					require.ErrorAs(t, err, &limit)
					assert.ErrorIs(t, err, ntable.ErrLimit)
					assert.Equal(t, limit.Bound+1, limit.Observed)
				}
				return
			}
			fake := &fcallROFake{reply: []any{"SET", "demo", "7", "12", []any{}, []any{}}}
			cmd, err := ntable.QueueReadSet(context.Background(), fake, "demo", scope, 7)
			require.NoError(t, err)
			_, err = cmd.Result()
			require.NoError(t, err)
			require.Len(t, fake.args, 3)
			var wire map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(fake.args[1].(string)), &wire))
			if len(tc.fields) == 0 {
				assert.NotContains(t, wire, "fields")
			} else {
				expected, err := json.Marshal(tc.fields)
				require.NoError(t, err)
				assert.JSONEq(t, string(expected), string(wire["fields"]))
			}
			assert.Equal(t, "7", fake.args[2])
		})
	}
}
