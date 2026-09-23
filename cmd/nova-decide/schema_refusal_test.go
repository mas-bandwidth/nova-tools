package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// AN UNMIGRATED SCHEMA IS A REFUSAL, AND NEVER A `recorded` RECEIPT.
//
// Stella on #1925 at 788dc953: "Do not degrade the machinery receipt into old
// provider-shaped columns or invent confidence. A nontransactional warning or
// receipt is not compatibility… a clear refusal on old schema without a false
// `recorded` receipt."
//
// So the CLI must not print its line and then mention the failure, and must
// not print `receipt=recorded` for a row that was refused. It prints NOTHING
// on stdout, refuses with a reason of its own, and names the migration.
func TestAnUnmigratedDecisionsSchemaRefusesWithNoFalseReceipt(t *testing.T) {
	settled := []string{
		"security_shaped_package: yes",
		"design_defaults_taken: 2",
		"normative_spec_moved: yes",
		"holder_of_the_area: design-authority",
		"hold_is_open: yes",
	}
	answered := []string{
		"security_shaped_package: no",
		"design_defaults_taken: 1",
		"normative_spec_moved: no",
		"holder_of_the_area: design-authority",
		"hold_is_open: yes",
	}
	for _, tc := range []struct {
		name  string
		state []string
	}{
		{"the settled path, which writes its own row", settled},
		{"the answered path, which writes through the client", answered},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &unmigratedStore{}
			restore := decisionsOpener
			decisionsOpener = func(dsn string) (decide.DecisionDriver, error) { return store, nil }
			defer func() { decisionsOpener = restore }()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"answers":{"reader":{"type":"choice","choice":"child-review","confidence":1}}}`))
			}))
			defer srv.Close()
			t.Setenv("JEV_API_KEY", "test-key")

			var out, errb bytes.Buffer
			code := run([]string{
				"--questions", shippedReaderQuestions,
				"--state", readerStateFile(t, tc.state...),
				"--base-url", srv.URL, "--key-env", "JEV_API_KEY", "--floor", "0.50",
				"--dsn", "postgres://unmigrated/example",
			}, &out, &errb)

			if code != 2 {
				t.Fatalf("exit %d, want 2; stdout=%q stderr=%q", code, out.String(), errb.String())
			}
			if strings.Contains(out.String(), "receipt=recorded") {
				t.Errorf("a refused row was reported as recorded:\n%s", out.String())
			}
			if strings.TrimSpace(out.String()) != "" {
				t.Errorf("a refusal printed a decision line anyway:\n%s", out.String())
			}
			if !strings.Contains(errb.String(), "decisions-schema-unmigrated") {
				t.Errorf("the refusal has no reason of its own: %q", errb.String())
			}
			if !strings.Contains(errb.String(), "nova-decide migrate") {
				t.Errorf("the refusal does not name the prerequisite: %q", errb.String())
			}
			if store.appends == 0 {
				t.Error("the writer never tried, so the refusal proves nothing")
			}
		})
	}
}

// And the migrate verb exists, so the prerequisite every refusal names is a
// command somebody can actually run.
func TestTheMigrateVerbInstallsTheSchemaAndSaysSo(t *testing.T) {
	store := &migratableStore{}
	restore := decisionsOpener
	decisionsOpener = func(dsn string) (decide.DecisionDriver, error) { return store, nil }
	defer func() { decisionsOpener = restore }()

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "--dsn", "postgres://example/db"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if store.migrations != 1 {
		t.Errorf("the verb ran %d migrations, want 1", store.migrations)
	}
	if !strings.Contains(out.String(), "MIGRATE OK") {
		t.Errorf("the verb said nothing:\n%s", out.String())
	}

	// And with no DSN it refuses rather than guessing which database to touch.
	out.Reset()
	errb.Reset()
	t.Setenv("NOVA_DSN", "")
	if code := run([]string{"migrate"}, &out, &errb); code != 2 {
		t.Errorf("migrate with no --dsn exited %d, want 2", code)
	}
}

// A too-new schema refuses with a reason of its own, on the write path and on
// the migrate verb, and never prints a receipt.
func TestATooNewDecisionsSchemaRefusesWithItsOwnReason(t *testing.T) {
	restore := decisionsOpener
	defer func() { decisionsOpener = restore }()
	decisionsOpener = func(dsn string) (decide.DecisionDriver, error) { return &tooNewStore{}, nil }

	var out, errb bytes.Buffer
	if code := run([]string{"migrate", "--dsn", "postgres://example/db"}, &out, &errb); code != 2 {
		t.Fatalf("migrate on a too-new schema exited %d, want 2; stdout=%q", code, out.String())
	}
	if out.Len() != 0 {
		t.Errorf("a refused migrate printed on stdout: %q", out.String())
	}
	if !strings.Contains(errb.String(), "reason=decisions-schema-too-new") {
		t.Errorf("the refusal has no reason of its own: %q", errb.String())
	}

	errb.Reset()
	if code := refuseWrite(&errb, "DECIDE", "the decision was made", (&tooNewStore{}).Append(decide.DecisionRow{})); code != 2 {
		t.Fatalf("refuseWrite exited %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "reason=decisions-schema-too-new") || strings.Contains(errb.String(), "receipt=recorded") {
		t.Errorf("write refusal on a too-new schema: %q", errb.String())
	}
}

type tooNewStore struct{}

func (s *tooNewStore) Append(decide.DecisionRow) error { return decide.ErrDecisionsSchemaTooNew }
func (s *tooNewStore) Rows(string) ([]decide.DecisionRow, error) {
	return nil, decide.ErrDecisionsSchemaTooNew
}
func (s *tooNewStore) Close() error   { return nil }
func (s *tooNewStore) Migrate() error { return decide.ErrDecisionsSchemaTooNew }

// unmigratedStore is a decisions table that has not been migrated: every write
// is the typed refusal, and it counts the attempts so a test can tell "refused"
// from "never tried".
type unmigratedStore struct{ appends int }

func (s *unmigratedStore) Append(decide.DecisionRow) error {
	s.appends++
	return decide.ErrDecisionsSchemaUnmigrated
}
func (s *unmigratedStore) Rows(string) ([]decide.DecisionRow, error) {
	return nil, decide.ErrDecisionsSchemaUnmigrated
}
func (s *unmigratedStore) Close() error { return nil }

// migratableStore is a decisions table that has a schema to install.
type migratableStore struct{ migrations int }

func (s *migratableStore) Append(decide.DecisionRow) error           { return nil }
func (s *migratableStore) Rows(string) ([]decide.DecisionRow, error) { return nil, nil }
func (s *migratableStore) Close() error                              { return nil }
func (s *migratableStore) Migrate() error                            { s.migrations++; return nil }
