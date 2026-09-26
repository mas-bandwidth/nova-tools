package typedrec_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// TestParsePathsRefusalReadsTheGate: the three refusals SP.gate writes read
// back with their stream (a name may hold a space), an overlap's other
// streams and paths; any other why, and a refusal missing its stream,
// paths or first stream, is not one.
func TestParsePathsRefusalReadsTheGate(t *testing.T) {
	t.Parallel()
	r, ok := typedrec.ParsePathsRefusal("PATHS overlap paths=internal/x,internal/x/y.go stream=swarm: cards")
	if !ok || r.Stream != "swarm: cards" || len(r.Also) != 0 || r.Paths != "internal/x,internal/x/y.go" || r.Unbuilt || r.NotOpen {
		t.Fatalf("overlap: %v %+v", ok, r)
	}
	r, ok = typedrec.ParsePathsRefusal("PATHS overlap paths=lib/core,lib/core/x.go stream=s1|s2|s3")
	if !ok || r.Stream != "s1" || strings.Join(r.Also, ",") != "s2,s3" || r.Paths != "lib/core,lib/core/x.go" {
		t.Fatalf("overlap s1|s2|s3: %v %+v", ok, r)
	}
	r, ok = typedrec.ParsePathsRefusal("PATHS unbuilt stream=work")
	if !ok || !r.Unbuilt || r.NotOpen || r.Stream != "work" || r.Paths != "" || r.Also != nil {
		t.Fatalf("unbuilt: %v %+v", ok, r)
	}
	r, ok = typedrec.ParsePathsRefusal("PATHS notopen stream=swarm: cards")
	if !ok || !r.NotOpen || r.Unbuilt || r.Stream != "swarm: cards" {
		t.Fatalf("notopen: %v %+v", ok, r)
	}
	for _, why := range []string{
		"", "EXISTS task:x", "PATHS", "PATHS overlap", "PATHS overlap paths= stream=a", "PATHS overlap paths=a",
		"PATHS overlap paths=a stream=", "PATHS overlap paths=a stream=|s2", "PATHS unbuilt stream=", "PATHS unbuilt",
		"PATHS notopen stream=", "PATHS closed stream=a", "REFUSED PATHS unbuilt stream=work",
	} {
		if _, ok := typedrec.ParsePathsRefusal(why); ok {
			t.Errorf("ParsePathsRefusal(%q) read a refusal", why)
		}
	}
}
