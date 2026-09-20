package other

import "testing"

func TestOther(t *testing.T) {
	if Other() != 2 {
		t.Fatal("two")
	}
}
