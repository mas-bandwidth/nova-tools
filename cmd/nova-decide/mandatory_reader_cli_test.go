package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mandatory-reader machinery has to stand on the PRODUCTION path.
//
// Stella's r2 hold (#1925, e1198bab): MandatoryReader, RequiredReads,
// ConstrainRead and ReadState existed only in readers.go and its own tests.
// The real verb loaded the pair, called the provider and printed its answer
// verbatim, so a state carrying a settled security designation, two design
// defaults, moved normative text and an unresolved hold accepted
// `child-review` at confidence 1.00 with ONE provider call and exit 0. A rule
// nothing calls is prose.
//
// This control goes through run() -- the real CLI entry -- with the SHIPPED
// question/criteria pair, and a fake that counts its calls. A settled
// designation must cost zero calls at any confidence, and an answer that
// stands must still carry every read the evidence obliges.
const shippedReaderQuestions = "../../docs/decide/questions-reader.json"

func TestTheRealCLIConstrainsTheProviderAnswerWithTheMandatoryReaderMachinery(t *testing.T) {
	if _, err := os.Stat(shippedReaderQuestions); err != nil {
		t.Fatalf("the shipped reader pair is the production path: %v", err)
	}
	cases := []struct {
		name         string
		state        []string
		choice       string
		conf         float64
		wantCalls    int
		wantReader   string
		wantSource   string
		wantRequired string
		wantHold     string
		wantExit     int
	}{{
		name: "a settled security designation is taken with no provider asked, against an answer at 1.00",
		state: []string{
			"security_shaped_package: yes -- the card execs the reaper",
			"design_defaults_taken: 2",
			"normative_spec_moved: yes",
			"holder_of_the_area: design-authority",
			"hold_is_open: yes",
		},
		choice: "child-review", conf: 1.00,
		wantCalls: 0, wantReader: "security-designate", wantSource: "machinery",
		wantRequired: "design-authority,security-designate", wantHold: "open", wantExit: 0,
	}, {
		name: "the same designation against an answer at 0.10: confidence is not a lever in either direction",
		state: []string{
			"security_shaped_package: yes",
			"design_defaults_taken: 2",
			"normative_spec_moved: yes",
			"holder_of_the_area: design-authority",
			"hold_is_open: yes",
		},
		choice: "child-review", conf: 0.10,
		wantCalls: 0, wantReader: "security-designate", wantSource: "machinery",
		wantRequired: "design-authority,security-designate", wantHold: "open", wantExit: 0,
	}, {
		name: "an unresolved holder keeps its read, and the answer names who reads FIRST",
		state: []string{
			"security_shaped_package: no",
			"design_defaults_taken: 1",
			"normative_spec_moved: no",
			"holder_of_the_area: design-authority",
			"hold_is_open: yes",
		},
		choice: "child-review", conf: 1.00,
		wantCalls: 1, wantReader: "child-review", wantSource: "provider",
		wantRequired: "design-authority", wantHold: "open", wantExit: 0,
	}, {
		name: "a required design read survives an answer that names somebody else first",
		state: []string{
			"security_shaped_package: no",
			"design_defaults_taken: 0",
			"normative_spec_moved: yes",
			"holder_of_the_area: none",
			"hold_is_open: no",
		},
		choice: "child-review", conf: 1.00,
		wantCalls: 1, wantReader: "child-review", wantSource: "provider",
		wantRequired: "design-authority", wantHold: "-", wantExit: 0,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				fmt.Fprintf(w, `{"answers":{"reader":{"type":"choice","choice":%q,"confidence":%g}},"usage":{"input_tokens":11,"output_tokens":3}}`, tc.choice, tc.conf)
			}))
			defer srv.Close()
			t.Setenv("JEV_API_KEY", "test-key")

			var out, errb bytes.Buffer
			code := run([]string{
				"--questions", shippedReaderQuestions,
				"--state", readerStateFile(t, tc.state...),
				"--base-url", srv.URL, "--key-env", "JEV_API_KEY", "--floor", "0.50",
			}, &out, &errb)
			if code != tc.wantExit {
				t.Fatalf("exit %d, want %d; stdout=%q stderr=%q", code, tc.wantExit, out.String(), errb.String())
			}
			if calls != tc.wantCalls {
				t.Errorf("the provider was called %d time(s), want %d", calls, tc.wantCalls)
			}
			line := strings.TrimRight(out.String(), "\n") + " "
			for _, want := range []string{
				" reader=" + tc.wantReader + " ",
				" source=" + tc.wantSource + " ",
				" required=" + tc.wantRequired + " ",
				" hold=" + tc.wantHold + " ",
				" lifts_hold=false ",
			} {
				if !strings.Contains(line, want) {
					t.Errorf("the line does not carry %q:\n%s", strings.TrimSpace(want), line)
				}
			}
			if tc.wantSource == "machinery" && strings.Contains(line, "reader="+tc.choice+" ") {
				t.Errorf("the provider's answer was printed over a settled designation:\n%s", line)
			}
		})
	}
}

// And the negative control on the other side: a state whose holder is not a
// configured role is a refusal BEFORE the call, because no answer creates an
// owner.
func TestTheRealCLIRefusesAnUnconfiguredHolderBeforeCallingTheProvider(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"answers":{"reader":{"type":"choice","choice":"child-review","confidence":1}}}`))
	}))
	defer srv.Close()
	t.Setenv("JEV_API_KEY", "test-key")

	var out, errb bytes.Buffer
	code := run([]string{
		"--questions", shippedReaderQuestions,
		"--state", readerStateFile(t,
			"security_shaped_package: no",
			"design_defaults_taken: 0",
			"normative_spec_moved: no",
			"holder_of_the_area: the-person-down-the-hall",
			"hold_is_open: yes"),
		"--base-url", srv.URL, "--key-env", "JEV_API_KEY", "--floor", "0.50",
	}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2; stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if calls != 0 {
		t.Errorf("the provider was called %d time(s) before the holder was refused", calls)
	}
	if !strings.Contains(errb.String(), "the-person-down-the-hall") {
		t.Errorf("the refusal does not name the holder it could not configure: %s", errb.String())
	}
}

// readerStateFile writes a state carrying every typed field the shipped reader
// question declares, with the case's own facts appended.
func readerStateFile(t *testing.T, lines ...string) string {
	t.Helper()
	body := append([]string{
		"kind: fix-with-red-test",
		"files: 4",
		"packages: 1",
		"test_added: yes",
		"pre_existing_tests_repaired: 0",
		"attempt: 1",
	}, lines...)
	path := filepath.Join(t.TempDir(), "state.md")
	write(t, path, strings.Join(body, "\n")+"\n")
	return path
}
