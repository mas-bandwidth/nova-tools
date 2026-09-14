package swarm

import (
	"strconv"
	"strings"
	"testing"
)

func TestOpenCodeInvalidNumericUsageIsAReadFailure(t *testing.T) {
	fakeSQLite3(t)
	max := strconv.Itoa(int(^uint(0) >> 1))
	for _, tc := range []struct{ name, rows string }{
		{"negative", "private-provider\tm\t-1\t0\t\t\t\n"},
		{"malformed", "private-provider\tm\tprivate-cell\t0\t\t\t\n"},
		{"out_of_range", "private-provider\tm\t9223372036854775808\t0\t\t\t\n"},
		{"column_overflow", "private-provider\tm\t" + max + "\t0\t\t\t\nprivate-provider\tm\t1\t0\t\t\t\n"},
		{"total_overflow", "private-provider\tm\t" + max + "\t1\t\t\t\n"},
		{"truncated_row", "private-provider\tm\t1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeDB(t, home, tc.rows)
			u, err := ReadProviderUsage(UsageOpenCode, home)
			if err == nil {
				t.Fatalf("corrupt source must fail instead of producing observed usage: %+v", u)
			}
			if u.Observed || len(u.Values) != 0 {
				t.Fatalf("a failed source must return no observed partial map: %+v", u)
			}
			if !strings.Contains(err.Error(), "row ") {
				t.Errorf("the refusal identifies its row: %v", err)
			}
			if !strings.Contains(err.Error(), "column") {
				t.Errorf("the refusal identifies its column: %v", err)
			}
			if strings.Contains(err.Error(), "private-provider") || strings.Contains(err.Error(), "private-cell") {
				t.Errorf("the refusal must not echo source data: %v", err)
			}
		})
	}
}

func TestOpenCodeNumericBoundsPreserveZeroAndAbsence(t *testing.T) {
	fakeSQLite3(t)
	max := strconv.Itoa(int(^uint(0) >> 1))
	home := t.TempDir()
	writeDB(t, home, "p\tm\t"+max+"\t0\t\t-\t\n")

	u, err := ReadProviderUsage(UsageOpenCode, home)
	if err != nil {
		t.Fatalf("host integer boundary is valid: %v", err)
	}
	if !u.Observed {
		t.Fatal("a numeric row is observed")
	}
	if got, _, _ := u.Sum(); strconv.Itoa(got) != max {
		t.Fatalf("downstream total at the host boundary = %d, want %s", got, max)
	}
	if got, _, _ := u.Budget(); strconv.Itoa(got) != max {
		t.Fatalf("downstream budget at the host boundary = %d, want %s", got, max)
	}
	for _, tc := range []struct{ column, want string }{
		{"tokens_in", max},
		{"tokens_out", "0"},
		{"cache_write", Dash},
		{"cache_read", Dash},
		{"reasoning", Dash},
	} {
		if got := u.Values[tc.column]; got != tc.want {
			t.Errorf("%s = %q, want %q", tc.column, got, tc.want)
		}
	}
}
