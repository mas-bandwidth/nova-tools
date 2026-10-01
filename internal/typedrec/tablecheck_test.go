package typedrec_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

func TestParseTableCheck(t *testing.T) {
	t.Parallel()
	got, err := typedrec.ParseTableCheck([]any{"CHECK", "18446744073709551615", "9007199254740993", "2", "3"})
	if err != nil || got.Epoch != 18446744073709551615 || got.Revision != 9007199254740993 || got.Members != 2 || got.Cells != 3 {
		t.Fatalf("report=%+v %v", got, err)
	}
	for _, reply := range [][]any{
		nil, {"CHECK"}, {"OK", "0", "0", "0", "0"}, {"CHECK", int64(0), "0", "0", "0"},
		{"CHECK", "0", "18446744073709551616", "0", "0"}, {"CHECK", "0", "0", "-1", "0"},
		{"CHECK", "0", "0", "0", "01"}, {"CHECK", "+1", "0", "0", "0"},
		{"CHECK", "0", "0", "0", "0", "extra"},
	} {
		if got, err := typedrec.ParseTableCheck(reply); err == nil || got != (typedrec.TableCheck{}) {
			t.Errorf("malformed reply %v produced %+v %v", reply, got, err)
		}
	}
}
