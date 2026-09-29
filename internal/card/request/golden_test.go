package request

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites the expected files under testdata from the current code:
//
//	go test ./internal/card/request -run Golden -update
var update = flag.Bool("update", false, "rewrite the golden expectations under testdata")

func expectFile(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s differs\n got  %s\n want %s", path, got, want)
	}
}

func TestGoldenRequests(t *testing.T) {
	t.Parallel()
	all, err := filepath.Glob("testdata/*.json")
	var inputs []string
	for _, in := range all {
		if !strings.HasPrefix(filepath.Base(in), "refuse-") {
			inputs = append(inputs, in)
		}
	}
	if err != nil || len(inputs) != 6 {
		t.Fatalf("golden inputs: %v %v", inputs, err)
	}
	for _, in := range inputs {
		in := in
		name := strings.TrimSuffix(filepath.Base(in), ".json")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			r, err := Parse(data)
			if err != nil {
				t.Fatalf("golden request refused: %v", err)
			}
			canon := Canonical(r)
			expectFile(t, "testdata/"+name+".canonical", append(canon, '\n'))
			expectFile(t, "testdata/"+name+".sha256", []byte(Hash(r)+"\n"))
			// The canonical form is a fixed point of parse and canonicalise.
			r2, err := Parse(canon)
			if err != nil || string(Canonical(r2)) != string(canon) {
				t.Fatalf("canonical form is not a fixed point: %v", err)
			}
			if !SameRequest(canon, Canonical(r2)) {
				t.Fatal("a request differs from itself")
			}
		})
	}
}

func TestGoldenEventsCoverEveryEventTypeWithATransition(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/events.json")
	if err != nil {
		t.Fatal(err)
	}
	r := mustParse(t, string(data))
	seen := map[EventType]bool{}
	for _, e := range r.Events {
		seen[e.Type] = true
	}
	for _, et := range EventTypes {
		if len(SourceStates(et)) == 0 {
			continue // ci-green has no transition, so no valid request carries it
		}
		if !seen[et] {
			t.Errorf("golden events lack a %s event", et)
		}
	}
}

func TestGoldenRefusals(t *testing.T) {
	t.Parallel()
	inputs, err := filepath.Glob("testdata/refuse-*.json")
	if err != nil || len(inputs) != 7 {
		t.Fatalf("golden refusals: %v %v", inputs, err)
	}
	for _, in := range inputs {
		in := in
		name := strings.TrimSuffix(filepath.Base(in), ".json")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			r, err := Parse(data)
			if err == nil || r != nil {
				t.Fatalf("document accepted: %+v", r)
			}
			rs := err.(*Refusals)
			expectFile(t, "testdata/"+name+".txt", []byte(strings.Join(rs.Lines(), "\n")+"\n"))
		})
	}
}

func TestGoldenReceipts(t *testing.T) {
	t.Parallel()
	r, j := sampleReceipt(), sampleRejection()
	expectFile(t, "testdata/receipt.canonical", append(CanonicalReceipt(r), '\n'))
	expectFile(t, "testdata/receipt.line", []byte(r.Line()+"\n"))
	expectFile(t, "testdata/rejection.canonical", append(CanonicalRejection(j), '\n'))
	expectFile(t, "testdata/rejection.line", []byte(j.Line()+"\n"))
}
