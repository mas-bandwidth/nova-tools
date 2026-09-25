package main

import (
	"bytes"
	"os"
	"testing"
)

func TestRoadmapXY(t *testing.T) {
	sexp := `(:schema "nova-work-roadmap-baseline-1"
 :verification (:by-feature (
   (:feature "E01-F01" :verified 1 :total 2 :tests ""
    :criteria ((:id "E01-F01-01" :state "verified" :text "A")
               (:id "E01-F01-02" :state "unverified" :text "B")))
   (:feature "E01-F02" :verified 2 :total 2 :tests ""
    :criteria ((:id "E01-F02-01" :state "verified" :text "C")
               (:id "E01-F02-02" :state "verified" :text "D")))
   (:feature "E02-F01" :verified 0 :total 1 :tests ""
    :criteria ((:id "E02-F01-01" :state "unverified" :text "E")))
 )))`
	path := t.TempDir() + "/nova-work.sexp"
	os.WriteFile(path, []byte(sexp), 0644)

	features, err := readRoadmapByFeature(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var buf bytes.Buffer
	code := printRoadmapXY(&buf, features, "")
	if code != 0 {
		t.Errorf("print failed, code %d", code)
	}
	out := buf.String()

	// "features verified/total" -> how many features have verified == total?
	// E01-F01 is 1/2. E01-F02 is 2/2. E02-F01 is 0/1. So 1 feature is fully verified. Total features: 3. So 1/3 features verified.
	// criteria verified/total -> 1 + 2 + 0 = 3 verified. Total criteria: 2 + 2 + 1 = 5. So 3/5 criteria verified.
	// We'll see. Let's just output for now and inspect.
	t.Logf("empty filter:\n%s", out)

	buf.Reset()
	code = printRoadmapXY(&buf, features, "E01") // epic rollup
	if code != 0 {
		t.Errorf("print epic failed, code %d", code)
	}
	t.Logf("E01 filter:\n%s", buf.String())

	// Test state change failure
	sexp2 := `(:schema "nova-work-roadmap-baseline-1"
 :verification (:by-feature (
   (:feature "E01-F01" :verified 1 :total 2 :tests ""
    :criteria ((:id "E01-F01-01" :state "unverified" :text "A")  ; CHANGED to unverified!
               (:id "E01-F01-02" :state "unverified" :text "B")))
 )))`
	path2 := t.TempDir() + "/nova-work2.sexp"
	os.WriteFile(path2, []byte(sexp2), 0644)

	features2, err := readRoadmapByFeature(path2)
	// either read fails, or print fails, or print gives 0/2.
	if err == nil {
		buf.Reset()
		code = printRoadmapXY(&buf, features2, "E01-F01")
		t.Logf("changed state output (code %d):\n%s", code, buf.String())
		if buf.String() == "E01-F01 1/2 50%\n" {
			t.Errorf("Printed stale counter even though criteria changed!")
		}
	} else {
		t.Logf("readRoadmapByFeature correctly refused: %v", err)
	}
}
