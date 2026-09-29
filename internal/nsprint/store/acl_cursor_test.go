package store_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// The receipt reader calls TYPE, HGET and XRANGE inside FCALL_RO. The table
// seat needs each underlying command as well as permission for the function;
// the coordinator also needs the schema-2 writer. Other seats do not receive
// either new table function by accident.
func TestReceiptCursorAndMultiBatchACLGrants(t *testing.T) {
	t.Parallel()
	roles := map[string]map[string]bool{}
	for _, rule := range store.ACLRules {
		parts := strings.Fields(rule)
		if len(parts) == 0 {
			t.Fatal("empty ACL rule")
		}
		grants := map[string]bool{}
		for _, part := range parts[1:] {
			grants[part] = true
		}
		roles[parts[0]] = grants
	}
	for _, role := range []string{"ns-coordinator", "ns-table"} {
		g := roles[role]
		for _, needed := range []string{"+type", "+hget", "+xrange", "+fcall_ro|ns_table_receipts"} {
			if !g[needed] {
				t.Errorf("%s lacks %s for the receipt reader", role, needed)
			}
		}
		if !g["~table:*"] && !g["%R~table:*"] {
			t.Errorf("%s cannot read table keys", role)
		}
	}
	if !roles["ns-coordinator"]["+fcall|ns_table_apply_multi"] {
		t.Error("coordinator lacks the multi-table writer")
	}
	if roles["ns-table"]["+fcall|ns_table_apply_multi"] {
		t.Error("read-only table seat can call the multi-table writer")
	}
	for _, role := range []string{"ns-friend", "ns-bench", "ns-reconciler", "ns-consumer"} {
		if roles[role]["+fcall_ro|ns_table_receipts"] || roles[role]["+fcall|ns_table_apply_multi"] {
			t.Errorf("%s received table receipt or writer function grant", role)
		}
	}
}
