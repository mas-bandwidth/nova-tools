package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEveryVerbHelpExits2(t *testing.T) {
	// We need a list of all 51 verbs. We can extract them from the usage string!
	var verbs []string
	for _, line := range strings.Split(usage, "\n") {
		if strings.HasPrefix(line, "  nova-work ") {
			verb := strings.TrimSpace(line)
			verb = strings.TrimPrefix(verb, "nova-work ")
			// take the verb part: could be 1 or 2 words depending if it's a family subverb
			// e.g. "session start", "node add", "events", "version"
			
			// actually, just split by spaces and take the tokens that don't start with - or < or (
			fields := strings.Fields(verb)
			v := fields[0]
			if len(fields) > 1 && !strings.HasPrefix(fields[1], "-") && !strings.HasPrefix(fields[1], "<") && !strings.HasPrefix(fields[1], "(") && !strings.HasPrefix(fields[1], "[") {
				if fields[1] != "check" && fields[1] != "expand" && fields[1] != "record" && fields[1] != "list" {
				    // handle families carefully
				    if v == "session" || v == "node" || v == "savepoint" || v == "operation" || v == "execution" || v == "goal" || v == "roadmap" {
					    v = v + " " + fields[1]
				    }
                }
                // actually wait, look at the usage string:
                // nova-work plan check -> "plan check"
                // nova-work set check -> "set check"
                // nova-work attempt record -> "attempt record"
                if v == "plan" || v == "set" || v == "attempt" {
                    v = v + " " + fields[1]
                }
			}
			verbs = append(verbs, v)
		}
	}

	// deduplicate
	seen := make(map[string]bool)
	var uniqueVerbs []string
	for _, v := range verbs {
		if !seen[v] {
			seen[v] = true
			uniqueVerbs = append(uniqueVerbs, v)
		}
	}
	
	// Ensure we also test family verbs alone
	families := []string{"execution", "goal", "node", "operation", "roadmap", "savepoint", "session"}
	for _, f := range families {
	    if !seen[f] {
	        seen[f] = true
	        uniqueVerbs = append(uniqueVerbs, f)
	    }
	}

	// Wait, the prompt says exactly 51 verbs. Let's see how many we got.
	t.Logf("Found %d verbs", len(uniqueVerbs))

	for _, v := range uniqueVerbs {
		t.Run(v, func(t *testing.T) {
			args := strings.Fields(v)
			args = append(args, "--help")
			var out bytes.Buffer
			code := run(args, &out, &out)
			if code != 2 {
				t.Errorf("expected exit code 2, got %d. Output: %s", code, out.String())
			}
			
			// Check if output contains the verb
			outStr := out.String()
			if !strings.Contains(outStr, v) {
				t.Errorf("output missing verb %q. Output: %s", v, outStr)
			}
			if strings.Contains(outStr, "flag: help requested") {
				t.Errorf("output still contains 'flag: help requested'")
			}
		})
	}
}
