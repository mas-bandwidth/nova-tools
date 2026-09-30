package fn

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Keep this inventory independent of the assembler. A new registration in the
// unchanged legacy module needs an explicit boundary review, even if the Lua
// shim would otherwise drop it.
var legacyRegistrations = []string{
	"ns_oset_move",
	"ns_table_apply",
	"ns_table_bind",
	"ns_table_cell_add",
	"ns_table_cell_move",
	"ns_table_cell_remove",
	"ns_table_check",
	"ns_table_clear",
	"ns_table_create",
	"ns_table_drop",
	"ns_table_drop_definition",
	"ns_table_list",
	"ns_table_member_create",
	"ns_table_member_find",
	"ns_table_members",
	"ns_table_read",
	"ns_table_read_set",
	"ns_table_row_add",
	"ns_table_row_del",
	"ns_table_row_set",
	"ns_table_rows_add",
	"ns_table_rows_hide",
	"ns_table_set",
	"ns_view_del",
	"ns_view_get",
	"ns_view_list",
	"ns_view_set",
}

var legacyStringRegistration = regexp.MustCompile(`redis\.register_function\(\s*'([^']+)'`)
var legacyTableRegistration = regexp.MustCompile(`redis\.register_function\s*\{\s*function_name\s*=\s*'([^']+)'`)

func TestTSetLegacyRegistrationBoundary(t *testing.T) {
	t.Parallel()
	body, err := sources.ReadFile("lua/table.lua")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, match := range legacyStringRegistration.FindAllStringSubmatch(string(body), -1) {
		found = append(found, match[1])
	}
	for _, match := range legacyTableRegistration.FindAllStringSubmatch(string(body), -1) {
		found = append(found, match[1])
	}
	sort.Strings(found)
	if strings.Join(found, "\n") != strings.Join(legacyRegistrations, "\n") {
		t.Fatalf("legacy registrations changed; review tset allowlist\nfound: %v\nwant: %v", found, legacyRegistrations)
	}

	allowed := make(map[string]bool, len(tsetLegacyReaders))
	for _, name := range tsetLegacyReaders {
		if allowed[name] {
			t.Fatalf("duplicate allowed reader %s", name)
		}
		allowed[name] = true
	}
	for _, line := range strings.Split(string(body), "\n") {
		matches := legacyTableRegistration.FindStringSubmatch(line)
		if matches == nil || !allowed[matches[1]] {
			continue
		}
		if !strings.Contains(line, "no-writes") {
			t.Fatalf("allowed reader %s lost its no-writes registration flag", matches[1])
		}
		delete(allowed, matches[1])
	}
	if len(allowed) != 0 {
		t.Fatalf("allowlist contains absent or unflagged readers: %v", allowed)
	}
}

func TestTSetProfileSourceIsExplicit(t *testing.T) {
	t.Parallel()
	if _, err := TSetSource("unsupported"); err == nil {
		t.Fatal("unknown profile accepted")
	}
	standalone, err := TSetSource(TSetStandalone)
	if err != nil {
		t.Fatal(err)
	}
	composed, err := TSetSource(TSetComposed)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		source  string
		profile string
		log     bool
	}{
		{"standalone", standalone, "l1_only", false},
		{"composed", composed, "composed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !strings.HasPrefix(tc.source, "#!lua name="+Library+"\nlocal NS = {tset_profile = '"+tc.profile+"'}\n") {
				t.Fatal("server-selected tset profile is absent from prelude")
			}
			if !strings.Contains(tc.source, "local function runtime_redis() return redis end\nlocal native_redis = redis\nlocal redis = {\n") ||
				!strings.Contains(tc.source, "name == 'ns_table_read'") {
				t.Fatal("legacy registration filter is absent")
			}
			for _, method := range []string{"call", "pcall", "sha1hex", "acl_check_cmd"} {
				if !strings.Contains(tc.source, method+" = function(...) return runtime_redis()."+method+"(...) end") {
					t.Errorf("%s must resolve the FCALL-time Redis facade", method)
				}
			}
			if strings.Contains(tc.source, "\n-- lua/table_set_log.lua\n") != tc.log {
				t.Fatal("Layer 2 fragment does not match profile")
			}
			for _, name := range tsetFragments {
				if name == "lua/table_set_log.lua" && !tc.log {
					continue
				}
				if strings.Count(tc.source, "\n-- "+name+"\n") != 1 {
					t.Fatalf("expected one %s fragment", name)
				}
			}
			if strings.Count(tc.source, "\n-- lua/table.lua\n") != 1 {
				t.Fatal("legacy reader module missing or repeated")
			}
			if strings.Contains(tc.source, "\n-- lua/sprint.lua\n") ||
				strings.Contains(tc.source, "\n-- lua/ws.lua\n") {
				t.Fatal("legacy writer module entered tset profile")
			}
		})
	}
}

func TestTSetLegacySourceKeepsFragmentsInert(t *testing.T) {
	t.Parallel()
	// Source still globs lua/*.lua for old tools. Every new fragment must have
	// an outer profile guard so that this old loading path cannot register the
	// tset writer on an old-engine server.
	for _, name := range tsetFragments {
		body, err := sources.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		first := ""
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "--") {
				first = line
				break
			}
		}
		if first != "if NS.tset_profile then" {
			t.Errorf("%s first statement is %q, want outer tset profile guard", name, first)
		}
	}
}
