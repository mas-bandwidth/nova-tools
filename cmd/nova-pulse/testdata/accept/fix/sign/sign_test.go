package sign

import "testing"

func TestSign(t *testing.T) {
	if Sign(3) != 1 {
		t.Fatal("three")
	}
}

func TestSignZero(t *testing.T) {
	if Sign(0) != 0 {
		t.Fatal("zero")
	}
}
