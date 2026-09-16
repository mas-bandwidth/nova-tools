package swarm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// profileBase is a valid single-profile catalog, SPEC-SWARM-PROFILES's own example shape
// with the normative field table's required `execution` object added to the worker.
// The first %s sits before `"execution"` inside the worker object (the deadline member,
// or empty for a profile missing its limit); the second %s sits after it (extra members
// a test wants to inject, or empty).
const profileBase = `{
  "version": 1,
  "profiles": {
    "go-small": {
      "worker": {
        "name": "hosted-small",
        "usage": "opencode",
        "harness": "/opt/example/bin/harness",
        "harness_args": ["run", "--", "{prompt}"],
        "worker_dir": "/opt/example/worker",
        %s
        "execution": {"adapter": "opencode-native/1"}
        %s
      },
      "route": {
        "provider": "opencode-go",
        "endpoint": "https://go.invalid",
        "credentials": {
          "kind": "nova-secrets",
          "store": "/secure/example/store",
          "seat": "worker",
          "age_key": "/secure/example/worker.agekey",
          "sops": "/opt/example/bin/sops",
          "gate": "/opt/example/bin/nova-secrets",
          "launcher": "/opt/example/bin/isolated-worker-launcher"
        }
      },
      "env_var": "OPENCODE_GO_KEY",
      "model": "example-go-model",
      "allowed_models": ["example-go-model"],
      "prompt": {"mode": "compact", "prefix": "Use the bounded task contract.", "tools": []}
    }
  }
}`

func profileJSON(deadline, extraWorker string) string {
	if deadline != "" {
		deadline = fmt.Sprintf(`"deadline": %q,`, deadline)
	}
	return fmt.Sprintf(profileBase, deadline, extraWorker)
}

// writeProfile writes one profile file and returns its path.
func writeProfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// mustRefuse loads the profile and requires a *ProfileRefusal whose Field names the given
// substring -- the loader's refusals name the field, and that is what these tests check.
func mustRefuse(t *testing.T, body, fieldSubstr string) {
	t.Helper()
	_, err := LoadProfile(writeProfile(t, body))
	if err == nil {
		t.Fatalf("expected a refusal naming %q, got none", fieldSubstr)
	}
	var ref *ProfileRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("expected a *ProfileRefusal, got %T: %v", err, err)
	}
	if !strings.Contains(ref.Field, fieldSubstr) {
		t.Fatalf("refusal field %q does not name %q", ref.Field, fieldSubstr)
	}
}

// TestProfileRefusesUnknownField: a profile whose worker carries a field the spec forbids
// there -- `key_file` is on the forbidden list -- is an exit-2 refusal naming that field.
func TestProfileRefusesUnknownField(t *testing.T) {
	mustRefuse(t, profileJSON("5m", `, "key_file": "/secure/example/key"`), "worker.key_file")
}

// TestProfileRefusesMissingLimit: a worker with no `deadline` is the missing-limits refusal,
// and it names the exact field a person must go and fill in.
func TestProfileRefusesMissingLimit(t *testing.T) {
	windowsIsNotABench(t)
	mustRefuse(t, profileJSON("", ""), "worker.deadline")
}

// TestProfilePreimageIsStable: the preimage hashes exactly the six binding fields of the
// profile record in canonical field order, so the same record (even reordered and
// re-spaced) hashes the same, and changing one field changes the hash.
func TestProfilePreimageIsStable(t *testing.T) {
	windowsIsNotABench(t)
	valid := profileJSON("5m", "")

	first, err := LoadProfile(writeProfile(t, valid))
	if err != nil {
		t.Fatalf("load valid profile: %v", err)
	}
	hashA, err := first.Preimage()
	if err != nil {
		t.Fatalf("preimage: %v", err)
	}
	if !strings.HasPrefix(hashA, "sha256:") || len(hashA) != 7+64 {
		t.Fatalf("preimage %q is not sha256:<64 hex>", hashA)
	}

	// Same input again: same hash.
	again, err := LoadProfile(writeProfile(t, valid))
	if err != nil {
		t.Fatalf("reload valid profile: %v", err)
	}
	hashAgain, err := again.Preimage()
	if err != nil {
		t.Fatalf("preimage again: %v", err)
	}
	if hashAgain != hashA {
		t.Fatalf("same profile preimage changed: got %s then %s", hashA, hashAgain)
	}

	// Reordered members and whitespace: still the same hash (canonical field order).
	reordered := `{
  "profiles": {
    "go-small": {
      "prompt": {"tools": [], "prefix": "Use the bounded task contract.", "mode": "compact"},
      "allowed_models": ["example-go-model"],
      "model": "example-go-model",
      "env_var": "OPENCODE_GO_KEY",
      "route": {"credentials": {"launcher": "/opt/example/bin/isolated-worker-launcher", "gate": "/opt/example/bin/nova-secrets", "sops": "/opt/example/bin/sops", "age_key": "/secure/example/worker.agekey", "seat": "worker", "store": "/secure/example/store", "kind": "nova-secrets"}, "endpoint": "https://go.invalid", "provider": "opencode-go"},
      "worker": {"execution": {"adapter": "opencode-native/1"}, "deadline": "5m", "worker_dir": "/opt/example/worker", "harness_args": ["run", "--", "{prompt}"], "harness": "/opt/example/bin/harness", "usage": "opencode", "name": "hosted-small"}
    }
  },
  "version": 1
}`

	reorderedProfile, err := LoadProfile(writeProfile(t, reordered))
	if err != nil {
		t.Fatalf("load reordered profile: %v", err)
	}
	hashReordered, err := reorderedProfile.Preimage()
	if err != nil {
		t.Fatalf("preimage reordered: %v", err)
	}
	if hashReordered != hashA {
		t.Fatalf("reordering changed the preimage: got %s, want %s", hashReordered, hashA)
	}

	// One field changed: a different hash.
	changed, err := LoadProfile(writeProfile(t, profileJSON("10m", "")))
	if err != nil {
		t.Fatalf("load changed profile: %v", err)
	}
	hashChanged, err := changed.Preimage()
	if err != nil {
		t.Fatalf("preimage changed: %v", err)
	}
	if hashChanged == hashA {
		t.Fatalf("a changed field produced the same preimage %s", hashA)
	}
}

// TestProfileRefusesEmptyID: a profile file whose only entry has an empty id is refused,
// naming the id.
func TestProfileRefusesEmptyID(t *testing.T) {
	body := `{
  "version": 1,
  "profiles": {
    "": {
      "worker": {
        "name": "hosted-small", "usage": "opencode",
        "harness": "/opt/example/bin/harness",
        "harness_args": ["run", "--", "{prompt}"],
        "worker_dir": "/opt/example/worker", "deadline": "5m",
        "execution": {"adapter": "opencode-native/1"}
      },
      "route": {"provider": "opencode-go", "credentials": {
        "kind": "nova-secrets", "store": "/s", "seat": "worker", "age_key": "/s/a",
        "sops": "/s/s", "gate": "/s/g", "launcher": "/s/l"
      }},
      "env_var": "OPENCODE_GO_KEY", "model": "example-go-model",
      "allowed_models": ["example-go-model"],
      "prompt": {"mode": "compact", "prefix": "Use the bounded task contract.", "tools": []}
    }
  }
}`
	mustRefuse(t, body, "profiles.")
}

// TestProfileRefusesDuplicateID: a catalog member repeated under the same id is a
// duplicate key, refused with that member named.
func TestProfileRefusesDuplicateID(t *testing.T) {
	body := `{
  "version": 1,
  "profiles": {
    "go-small": {"worker": {"name": "a"}},
    "go-small": {"worker": {"name": "b"}}
  }
}`
	mustRefuse(t, body, "go-small")
}
