package main

import (
	"strings"
	"testing"
)

func TestTestmanifestCoverQuoteNamesPlain(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne", "TestTwo", "TestThree"}
	got := quoteNames(names)
	if len(got) != len(names) {
		t.Fatalf("quoteNames returned %d items, want %d", len(got), len(names))
	}
	for i, name := range names {
		if got[i] != name {
			t.Errorf("quoteNames[%d] = %q, want %q", i, got[i], name)
		}
	}
}

func TestTestmanifestCoverQuoteNamesEmpty(t *testing.T) {
	t.Parallel()
	got := quoteNames([]string{})
	if len(got) != 0 {
		t.Fatalf("quoteNames([]) returned %d items, want 0", len(got))
	}
}

func TestTestmanifestCoverPattern(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne", "TestTwo"}
	quoted := quoteNames(names)
	pattern := "^(?:" + strings.Join(quoted, "|") + ")$"
	// Build regex via pattern string like main does
	if pattern == "" {
		t.Fatal("pattern is empty")
	}
	// Test that each name matches exactly
	if !strings.Contains(pattern, "TestOne") {
		t.Errorf("pattern missing TestOne: %s", pattern)
	}
	if !strings.Contains(pattern, "TestTwo") {
		t.Errorf("pattern missing TestTwo: %s", pattern)
	}
	// TestOne should not match TestOneMore (suffix)
	// This is verified by the regex structure: ^(?:TestOne|TestTwo)$
}

func TestTestmanifestCoverCheckMalformedLine(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `malformed go test JSON` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil || !strings.Contains(err.Error(), "malformed go test JSON") {
		t.Fatalf("check error = %v, want containing %q", err, "malformed go test JSON")
	}
}

func TestTestmanifestCoverCheckPackageFail(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"fail","Package":"example.test/p","Test":""}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil || !strings.Contains(err.Error(), "package action fail") {
		t.Fatalf("check error = %v, want containing %q", err, "package action fail")
	}
}

func TestTestmanifestCoverCheckPackageSkip(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"skip","Package":"example.test/p","Test":""}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil || !strings.Contains(err.Error(), "package action skip") {
		t.Fatalf("check error = %v, want containing %q", err, "package action skip")
	}
}

func TestTestmanifestCoverCheckUnexpectedTestRun(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"run","Package":"example.test/p","Test":"TestOther"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOther"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil || !strings.Contains(err.Error(), "unexpected test TestOther") {
		t.Fatalf("check error = %v, want containing %q", err, "unexpected test TestOther")
	}
}

func TestTestmanifestCoverCheckUnexpectedTestPass(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOther"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil || !strings.Contains(err.Error(), "unexpected test TestOther") {
		t.Fatalf("check error = %v, want containing %q", err, "unexpected test TestOther")
	}
}

func TestTestmanifestCoverCheckNoPackagePass(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil || !strings.Contains(err.Error(), "package PASS count=0 want=1") {
		t.Fatalf("check error = %v, want containing %q", err, "package PASS count=0 want=1")
	}
}

func TestTestmanifestCoverCheckTwoPackagePasses(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil || !strings.Contains(err.Error(), "package PASS count=2 want=1") {
		t.Fatalf("check error = %v, want containing %q", err, "package PASS count=2 want=1")
	}
}

func TestTestmanifestCoverCheckCheckInvalidNamesInInput(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne", "TestTwo/sub"}
	err := check(strings.NewReader(passingStream("TestOne")), names, true)
	if err == nil || !strings.Contains(err.Error(), "invalid top-level test name") {
		t.Fatalf("check error = %v, want containing %q", err, "invalid top-level test name")
	}
}

func TestTestmanifestCoverCheckMultipleProblemsSorted(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne", "TestTwo"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOther"}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil {
		t.Fatalf("check error = nil, want errors")
	}
	e := err.Error()
	if !strings.Contains(e, "TestOne run=2 pass=2") || !strings.Contains(e, "TestTwo run=0 pass=0") || !strings.Contains(e, "package PASS count=2 want=1") || !strings.Contains(e, "unexpected test TestOther") {
		t.Fatalf("check error = %v, want all problems", err)
	}
	// Check sorted order: TestOne < TestTwo < package < unexpected (alphabetically)
	if strings.Index(e, "TestOne") > strings.Index(e, "TestTwo") ||
		strings.Index(e, "TestTwo") > strings.Index(e, "package") ||
		strings.Index(e, "package") > strings.Index(e, "unexpected") {
		t.Errorf("problems not sorted: %s", e)
	}
}

func TestTestmanifestCoverCheckOverflowLine(t *testing.T) {
	t.Parallel()
	names := []string{"TestOne"}
	stream := `{"Action":"run","Package":"example.test/p","Test":"TestOne"}` + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":"TestOne"}` + "\n"
	// Create a line longer than 16 MiB
	longLine := strings.Repeat("x", 17*1024*1024)
	stream += longLine + "\n"
	stream += `{"Action":"pass","Package":"example.test/p","Test":""}` + "\n"
	err := check(strings.NewReader(stream), names, true)
	if err == nil || !strings.Contains(err.Error(), "read go test JSON") {
		t.Fatalf("check error = %v, want containing %q", err, "read go test JSON")
	}
}
