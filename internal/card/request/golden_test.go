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
	if err != nil || len(inputs) != 7 {
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
			v, err := Parse(data)
			if err != nil {
				t.Fatalf("golden request refused: %v", err)
			}
			canon := v.Canonical()
			expectFile(t, "testdata/"+name+".canonical", append(canon, '\n'))
			expectFile(t, "testdata/"+name+".sha256", []byte(v.Hash()+"\n"))
			// The canonical form is a fixed point of parse and canonicalise.
			v2, err := Parse(canon)
			if err != nil || string(v2.Canonical()) != string(canon) {
				t.Fatalf("canonical form is not a fixed point: %v", err)
			}
			if !SameRequest(canon, v2) {
				t.Fatal("a request differs from itself")
			}
		})
	}
}

func TestGoldenInputsCoverEveryLifecycleInputType(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	r := mustParse(t, string(data)).Request()
	seen := map[InputType]bool{}
	for _, e := range r.Inputs {
		seen[e.Type] = true
	}
	for _, it := range InputTypes() {
		if !seen[it] {
			t.Errorf("golden inputs lack a %s input", it)
		}
	}
}

func TestGoldenEvidenceCoversEveryKindSubmitted(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	r := mustParse(t, string(data)).Request()
	seen := map[EvidenceKind]bool{}
	for _, e := range r.Evidence {
		for _, rc := range e.Records {
			seen[rc.Kind] = true
		}
	}
	for _, k := range EvidenceKinds() {
		if k != KindQueue && !seen[k] {
			t.Errorf("golden evidence lacks a %s record", k)
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
			v, err := Parse(data)
			if err == nil || v != nil {
				t.Fatalf("document accepted: %+v", v)
			}
			rs := err.(*Refusals)
			out := strings.Join(rs.Lines(), "\n") + "\n"
			if strings.Contains(out, "secret") {
				t.Fatalf("a refusal echoes the credential of an origin URL:\n%s", out)
			}
			expectFile(t, "testdata/"+name+".txt", []byte(out))
		})
	}
}

func TestGoldenReceipts(t *testing.T) {
	t.Parallel()
	r, j, ir := sampleReceipt(), sampleRejection(), sampleInspect()
	expectFile(t, "testdata/receipt.canonical", append(CanonicalReceipt(r), '\n'))
	expectFile(t, "testdata/receipt.line", []byte(r.Line()+"\n"))
	expectFile(t, "testdata/rejection.canonical", append(CanonicalRejection(j), '\n'))
	expectFile(t, "testdata/rejection.line", []byte(j.Line()+"\n"))
	expectFile(t, "testdata/inspect-result.canonical", append(ir.Canonical(), '\n'))
	rc := rec(KindCI, DispRed, "ci:unit", g40, "run:9003")
	expectFile(t, "testdata/record.line", []byte(rc.Line()+"\n"+rc.ID()+"\n"))
}
