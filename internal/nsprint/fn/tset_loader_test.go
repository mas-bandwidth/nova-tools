package fn

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Keep the old module's registration inventory independent of the tset
// assembler. The old library continues to load table.lua unchanged.
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
		t.Fatalf("legacy registrations changed\nfound: %v\nwant: %v", found, legacyRegistrations)
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
	checkTSetProfileSource(t, standalone, "l1_only", false)
}

// The composed source has a separate gate: a missing real Layer 2 fragment
// must not prevent the standalone source boundary from being checked.
func TestTSetComposedSourceIsExplicit(t *testing.T) {
	t.Parallel()
	composed, err := TSetSource(TSetComposed)
	if err != nil {
		t.Fatal(err)
	}
	checkTSetProfileSource(t, composed, "composed", true)
}

func checkTSetProfileSource(t *testing.T, source, profile string, log bool) {
	t.Helper()
	if !strings.HasPrefix(source, "#!lua name="+Library+"\nlocal NS = {tset_profile = '"+profile+"'}\n") {
		t.Fatal("server-selected tset profile is absent from prelude")
	}
	if !strings.Contains(source, "local function runtime_redis() return redis end\nlocal native_redis = redis\nlocal redis = {\n") ||
		!strings.Contains(source, "if name == 'ns_tset_step' or name == 'ns_tset_read' then") {
		t.Fatal("exact tset registration filter is absent")
	}
	for _, name := range legacyRegistrations {
		if strings.Contains(source, "name == '"+name+"'") {
			t.Errorf("legacy callback %s entered tset registration filter", name)
		}
	}
	for _, method := range []string{"call", "pcall", "sha1hex", "acl_check_cmd"} {
		if !strings.Contains(source, method+" = function(...) return runtime_redis()."+method+"(...) end") {
			t.Errorf("%s must resolve the FCALL-time Redis facade", method)
		}
	}
	if strings.Contains(source, "\n-- lua/table_set_log.lua\n") != log {
		t.Fatal("Layer 2 fragment does not match profile")
	}
	for _, name := range tsetFragments {
		if name == "lua/table_set_log.lua" && !log {
			continue
		}
		if strings.Count(source, "\n-- "+name+"\n") != 1 {
			t.Fatalf("expected one %s fragment", name)
		}
	}
	for _, name := range []string{"lua/table.lua", "lua/sprint.lua", "lua/ws.lua"} {
		if strings.Contains(source, "\n-- "+name+"\n") {
			t.Errorf("legacy module %s entered tset profile", name)
		}
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
