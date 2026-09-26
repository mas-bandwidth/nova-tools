package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

func TestOwnerRefusalPrintsCauseAndReportsChangedOrRecurringFailure(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	as := taskcard.Consumer{Kind: "friend", Name: "fixture"}
	seen := map[string]string{}
	refused := []life.OwnerRefusal{{ID: "work~1", Why: "FENCED work~1 token changed"}}
	printOwnerRefusals(&out, as, "", refused, seen)
	for _, want := range []string{"FRIEND OWNER REFUSED", "as=friend:fixture", "id=work~1", "renewed=false", "FENCED work~1 token changed", "next=", "nova-sprint card render --id work~1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	out.Reset()
	printOwnerRefusals(&out, as, "", refused, seen)
	if out.Len() != 0 {
		t.Fatal("unchanged refusal repeated")
	}
	refused[0].Why = "STALE work~1 owner observation"
	printOwnerRefusals(&out, as, "", refused, seen)
	if !strings.Contains(out.String(), "STALE") {
		t.Fatal("changed cause hidden")
	}
	out.Reset()
	printOwnerRefusals(&out, as, "", nil, seen)
	printOwnerRefusals(&out, as, "", refused, seen)
	if !strings.Contains(out.String(), "STALE") {
		t.Fatal("recurrence after recovery hidden")
	}
}
