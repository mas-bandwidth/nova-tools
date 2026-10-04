package typedrec_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

func TestCardPathsUsesTheHeaderFieldsForScope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		fields map[string]string
		want   []string
	}{
		{"absent", nil, nil},
		{"new only", map[string]string{"NEW": "added.go"}, []string{"added.go"}},
		{"normalized order", map[string]string{"PATHS": " , a.go, none, b.go ,", "NEW": " c.go, , none "}, []string{"a.go", "b.go", "c.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := typedrec.CardPaths(func(key string) (string, bool) {
				value, ok := tc.fields[key]
				return value, ok
			})
			assert.Equal(t, tc.want, got)
		})
	}
}
