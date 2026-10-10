package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
)

// Tables reads the stored table names of an OpRecord's manifests in order.
// The unit tier never reached it (a reader's finding: one function at 0.0%
// in the per-function coverage table of the unit tier). These rows cover its
// main path and its empty answer: no sleep, no real time, no network, no store.

func TestBackendCoverTablesReturnsManifestOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		manifests  []ntable.BatchManifest
		want       []string
		wantNonNil bool
	}{
		{"no manifests returns a non-nil empty slice",
			nil,
			[]string{}, true},
		{"an empty manifest slice returns a non-nil empty slice",
			[]ntable.BatchManifest{},
			[]string{}, true},
		{"one manifest names its single table",
			[]ntable.BatchManifest{{Table: "t-work"}},
			[]string{"t-work"}, true},
		{"two manifests name their tables in order",
			[]ntable.BatchManifest{{Table: "t-work"}, {Table: "t-inbox"}},
			[]string{"t-work", "t-inbox"}, true},
		{"the order matches the manifest order",
			[]ntable.BatchManifest{{Table: "t-zeta"}, {Table: "t-alpha"}, {Table: "t-mid"}},
			[]string{"t-zeta", "t-alpha", "t-mid"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			op := OpRecord{ID: "op-1", Manifests: tt.manifests}
			got := op.Tables()
			assert.Equal(t, tt.want, got, tt.name)
			if tt.wantNonNil {
				assert.NotNil(t, got, "a nil result would break callers that range over it")
				assert.Len(t, got, len(tt.manifests), "the length matches the manifest count")
			}
		})
	}
}
