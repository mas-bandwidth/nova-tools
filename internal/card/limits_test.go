package card

import "testing"

// The relations between the table's bounds and the card layer's, and the
// worst-case manifest of every operation, stated as sums.
func TestBatchLimitsFitTheTable(t *testing.T) {
	t.Parallel()
	checks := []struct {
		name string
		ok   bool
	}{
		{"the table's bounds are 128, 1,024, 1 MiB, 256, 64 KiB and 128", TableChangedEntries == 128 && TableGuardOnlyEntries == 1024 && TableManifestBytes == 1<<20 && TableMemberIDBytes == 256 && TableFieldValueBytes == 64<<10 && TableSetFieldsPerMember == 128},
		{"the operation record takes one changed entry, so a request has 127", MaxChangedEntries == 127 && MaxChangedEntries+CardOperationRecordEntries == TableChangedEntries},
		{"63 replacement pairs are 126 members and the record", MaxReplacementPairs == 63 && 2*MaxReplacementPairs+CardOperationRecordEntries <= TableChangedEntries},
		{"a resolve of 113 cards with 8 dependencies each needs 1,017 guard entries", MaxResolveCards == 113 && MaxResolveCards*(1+MaxDependsOn) == 1017 && MaxResolveCards*(1+MaxDependsOn) <= TableGuardOnlyEntries},
		{"127 admissions of 8 dependencies name at most 1,016 outside cards", MaxChangedEntries*MaxDependsOn == 1016 && MaxChangedEntries*MaxDependsOn <= MaxOutsideDependencies && MaxOutsideDependencies <= TableGuardOnlyEntries},
		{"a card ID fits a member ID", MaxIDBytes == 64 && MaxIDBytes <= TableMemberIDBytes},
		{"an operation ID fits a member ID after the op. prefix", MaxOperationIDBytes+3 <= TableMemberIDBytes},
		{"an admission record fits a field value", MaxAdmissionRecordBytes <= TableFieldValueBytes},
		{"the canonical request bound is the manifest bound", MaxCanonicalBytes == TableManifestBytes},
		{"evidence: 16 records a card, 512 in all", MaxEvidenceRecordsPerCard == 16 && MaxEvidenceRecords == 512},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: false", c.name)
		}
	}
}

// The sums of the comment in limits.go, one per operation.
func TestWorstCaseManifestSums(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ got, want int }{
		"admit":    {ManifestAdmitMax, 1024 + 286131 + 246784},
		"replace":  {ManifestReplaceMax, 1024 + 174195 + 246784},
		"inputs":   {ManifestInputsMax, 1024 + 572262 + 246784},
		"evidence": {ManifestEvidenceMax, 1024 + 796083 + 246784},
		"resolve":  {ManifestResolveMax, 1024 + 295156 + 246784},
	} {
		if c.got != c.want {
			t.Errorf("%s: %d, the comment says %d", name, c.got, c.want)
		}
		if c.got > TableManifestBytes {
			t.Errorf("%s: %d exceeds the table's %d", name, c.got, TableManifestBytes)
		}
	}
	if ManifestEvidenceMax != 1043891 || TableManifestBytes-ManifestEvidenceMax != 4685 {
		t.Errorf("the evidence margin is %d bytes", TableManifestBytes-ManifestEvidenceMax)
	}
}
