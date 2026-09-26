package fleet_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// TestFleetOneWriter is a grep class test: bench:*:state and bench:*:hold are written only in fleet.lua,
// and cfg:fleet only by ns_fleet_config.
func TestFleetOneWriter(t *testing.T) {
	t.Parallel()

	source, err := fn.Source()
	if err != nil {
		t.Fatalf("fn.Source(): %v", err)
	}
	sections := strings.Split(source, "\n-- lua/")
	writes := regexp.MustCompile(`redis\.call\('(HSET|HSETNX|HDEL|DEL|UNLINK|HINCRBY|HINCRBYFLOAT|EXPIRE|PEXPIRE|SET|RENAME|HMSET)',\s*([^,)]+)`)

	stateWrites := 0
	holdWrites := 0
	cfgWrites := 0

	binds := regexp.MustCompile(`local\s+(\w+)\s*=\s*'bench:'\s*\.\.\s*\w+\s*\.\.\s*':state'`)

	for _, sec := range sections[1:] {
		name := sec[:strings.Index(sec, "\n")]
		isFleetLua := name == "fleet.lua"

		inFleetConfig := false
		stateVars := map[string]bool{}
		for i, line := range strings.Split(sec, "\n") {
			if strings.HasPrefix(line, "local function ") || strings.HasPrefix(line, "function ") {
				inFleetConfig = strings.HasPrefix(line, "local function fleet_config")
				stateVars = map[string]bool{}
			}
			if m := binds.FindStringSubmatch(line); m != nil {
				stateVars[m[1]] = true
			}

			m := writes.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			target := strings.TrimSpace(m[2])

			if (strings.Contains(target, "'bench:'") && strings.Contains(target, "':state'")) || stateVars[target] {
				stateWrites++
				if !isFleetLua {
					t.Errorf("lua/%s line %d writes bench:*:state (%s) outside fleet.lua", name, i+1, strings.TrimSpace(line))
				}
			}

			if strings.Contains(target, "':hold'") {
				holdWrites++
				if !isFleetLua {
					t.Errorf("lua/%s line %d writes bench:*:hold (%s) outside fleet.lua", name, i+1, strings.TrimSpace(line))
				}
			}

			if strings.Contains(target, "'cfg:fleet'") {
				cfgWrites++
				if !isFleetLua || !inFleetConfig {
					t.Errorf("lua/%s line %d writes cfg:fleet (%s) outside fleet_config", name, i+1, strings.TrimSpace(line))
				}
			}
		}
	}

	if stateWrites == 0 {
		t.Fatal("found no write to bench:*:state; check regex")
	}
	if holdWrites == 0 {
		t.Fatal("found no write to bench:*:hold; check regex")
	}
	if cfgWrites == 0 {
		t.Fatal("found no write to cfg:fleet; check regex")
	}
}
