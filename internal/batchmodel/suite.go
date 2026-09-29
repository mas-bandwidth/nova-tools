package batchmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
)

// CaptureSuiteOptions contains caller-owned inputs. CaptureSuite starts only
// disposable loopback Redis servers and writes TLC packets; it never downloads
// tools or invokes Java. OutputDir must be private to this invocation.
type CaptureSuiteOptions struct {
	Source, RedisServer, ModelsDir, OutputDir, TmpDir string
}

type SuiteCase struct {
	Name           string            `json:"name"`
	Bundle         Bundle            `json:"bundle"`
	Expected       string            `json:"expected"` // pass or invariant-reject
	Property       string            `json:"property"`
	SourceSHA256   string            `json:"source_sha256"`
	EvidenceSHA256 string            `json:"evidence_sha256"`
	ModelsSHA256   map[string]string `json:"models_sha256"`
}

const finiteFields = `{"order":"c1,c2","footer":"total","created_at":"2026-09-27T00:00:00Z","epoch_key":"replay:epoch","epoch_field":"n","col:c1":"members:none:10:c1","col:c2":"members:none:10:c2"}`

// CaptureSuite captures three continuous real batch histories plus a
// deliberately corrupted independent post-observation. The negative packet
// must be rejected specifically by MatchesExecution when TLC executes it.
func CaptureSuite(ctx context.Context, opts CaptureSuiteOptions) ([]SuiteCase, error) {
	if opts.Source == "" || opts.RedisServer == "" || opts.ModelsDir == "" || opts.OutputDir == "" {
		return nil, fmt.Errorf("suite needs source, Redis executable, models directory and output directory")
	}
	source, err := os.ReadFile(opts.Source)
	if err != nil {
		return nil, &tablemodel.CannotRun{Err: err}
	}
	if len(source) == 0 {
		return nil, fmt.Errorf("empty Lua source")
	}
	root, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		return nil, err
	}
	models, err := filepath.Abs(opts.ModelsDir)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, &tablemodel.CannotRun{Err: fmt.Errorf("suite output must be new and private: %w", err)}
	}
	hash := sha256.Sum256(source)
	fingerprint := hex.EncodeToString(hash[:])
	if err := os.WriteFile(filepath.Join(root, "source.lua"), source, 0o600); err != nil {
		return nil, &tablemodel.CannotRun{Err: err}
	}
	cases := make([]SuiteCase, 0, 6)
	for _, name := range []string{"move-retry-conflict", "noop-remove-unset", "create-preventive-refusal"} {
		if err := ctx.Err(); err != nil {
			return cases, err
		}
		var steps []Step
		var captureErr error
		err := tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: opts.RedisServer, TmpDir: opts.TmpDir}, func(r *tablemodel.Store) {
			captureErr = seedSuiteRuntime(r, source, name, true)
			if captureErr != nil {
				return
			}
			steps, _, captureErr = RunFiniteTrace(ctx, RedisCapture{Store: r}, suiteBuilders(name))
		})
		if err != nil {
			return cases, fmt.Errorf("%s store: %w", name, err)
		}
		if captureErr != nil {
			return cases, fmt.Errorf("%s capture: %w", name, captureErr)
		}
		bundle, err := WriteBundle(models, filepath.Join(root, name), steps)
		if err != nil {
			return cases, fmt.Errorf("%s bundle: %w", name, cannotRunIO(err))
		}
		positive, err := sealSuiteCase(name, bundle, "pass", fingerprint)
		if err != nil {
			return cases, err
		}
		cases = append(cases, positive)
		if name == "create-preventive-refusal" {
			bad := steps[0]
			bad.AfterModel, err = corruptCreatedStatus(bad.AfterModel)
			if err != nil {
				return cases, err
			}
			negative, err := WriteBundle(models, filepath.Join(root, "corrupt-observation"), []Step{bad})
			if err != nil {
				return cases, fmt.Errorf("negative bundle: %w", cannotRunIO(err))
			}
			control, err := sealSuiteCase("corrupt-observation", negative, "invariant-reject", fingerprint)
			if err != nil {
				return cases, err
			}
			cases = append(cases, control)
		}
	}
	if err := ctx.Err(); err != nil {
		return cases, err
	}
	var mixed []MixedStep
	var captureErr error
	err = tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: opts.RedisServer, TmpDir: opts.TmpDir}, func(r *tablemodel.Store) { mixed, captureErr = RunSecondEpoch(ctx, r, source) })
	if err != nil {
		return cases, fmt.Errorf("second-epoch store: %w", err)
	}
	if captureErr != nil {
		return cases, fmt.Errorf("second-epoch capture: %w", captureErr)
	}
	second, err := WriteMixedBundle(models, filepath.Join(root, "second-epoch"), mixed)
	if err != nil {
		return cases, fmt.Errorf("second-epoch bundle: %w", cannotRunIO(err))
	}
	secondCase, err := sealSuiteCase("second-epoch", second, "pass", fingerprint)
	if err != nil {
		return cases, err
	}
	cases = append(cases, secondCase)
	if err := ctx.Err(); err != nil {
		return cases, err
	}
	var ordinary []MixedStep
	captureErr = nil
	err = tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: opts.RedisServer, TmpDir: opts.TmpDir}, func(r *tablemodel.Store) { ordinary, captureErr = RunOrdinaryRemove(ctx, r, source) })
	if err != nil {
		return cases, fmt.Errorf("ordinary writer store: %w", err)
	}
	if captureErr != nil {
		return cases, fmt.Errorf("ordinary writer capture: %w", captureErr)
	}
	ordinaryBundle, err := WriteOrdinaryBundle(models, filepath.Join(root, "ordinary-remove-omitted-guard"), ordinary)
	if err != nil {
		return cases, fmt.Errorf("ordinary writer bundle: %w", cannotRunIO(err))
	}
	ordinaryCase, err := sealSuiteCase("ordinary-remove-omitted-guard", ordinaryBundle, "pass", fingerprint)
	if err != nil {
		return cases, err
	}
	cases = append(cases, ordinaryCase)
	manifest, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		return cases, err
	}
	if err := os.WriteFile(filepath.Join(root, "suite.json"), manifest, 0o600); err != nil {
		return cases, &tablemodel.CannotRun{Err: err}
	}
	return cases, nil
}

func cannotRunIO(err error) error {
	var path *os.PathError
	if errors.As(err, &path) {
		return &tablemodel.CannotRun{Err: err}
	}
	return err
}

func sealSuiteCase(name string, b Bundle, expected, fingerprint string) (SuiteCase, error) {
	c := SuiteCase{Name: name, Bundle: b, Expected: expected, Property: "MatchesExecution", SourceSHA256: fingerprint, ModelsSHA256: map[string]string{}}
	raw, err := os.ReadFile(b.Evidence)
	if err != nil {
		return c, &tablemodel.CannotRun{Err: err}
	}
	h := sha256.Sum256(raw)
	c.EvidenceSHA256 = hex.EncodeToString(h[:])
	err = filepath.WalkDir(b.Work, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(b.Work, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h := sha256.Sum256(raw)
		c.ModelsSHA256[rel] = hex.EncodeToString(h[:])
		return nil
	})
	if err != nil {
		return c, &tablemodel.CannotRun{Err: err}
	}
	return c, nil
}

func seedSuiteRuntime(r *tablemodel.Store, source []byte, name string, withGuard bool) error {
	r.Cmd("FUNCTION", "LOAD", "REPLACE", "#!lua name=batch_suite_"+strings.ReplaceAll(name, "-", "_")+"\n"+string(source))
	r.Cmd("HSET", "replay:epoch", "n", "1")
	call := func(name string, args ...any) any {
		return r.Cmd(append([]any{"FCALL", "ns_table_" + name, 0}, args...)...)
	}
	if got := call("create", "t1", finiteFields, `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
		return fmt.Errorf("create: %v", got)
	}
	for _, row := range []string{"r1", "r2"} {
		if got := call("row_add", "t1", row, "{}", `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[ROW ") {
			return fmt.Errorf("row %s: %v", row, got)
		}
	}
	if got := call("cell_add", "t1", "r1", "c1", 1, "m1", `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
		return fmt.Errorf("m1 seed: %v", got)
	}
	r.Cmd("HSET", ntable.MemberKey("m1"), "status", "ready")
	if withGuard {
		if got := call("cell_add", "t1", "r1", "c2", 2, "m3", `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
			return fmt.Errorf("m3 seed: %v", got)
		}
		r.Cmd("HSET", ntable.MemberKey("m3"), "token", "permit")
	}
	return nil
}

func suiteBuilders(name string) []RequestBuilder {
	switch name {
	case "move-retry-conflict":
		var original []byte
		return []RequestBuilder{
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				original = []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-move","actor":"w1","members":[{"id":"m1","expect":{"revision":%q,"place":{"row":"r1","col":"c1"},"fields":{"status":{"equals":"ready"}}},"move":{"row":"r2","col":"c1"},"set":{"status":"done"}},{"id":"m3","expect":{"revision":%q,"fields":{"token":{"equals":"permit"}}}}]}`, s.TableRevision, s.Members["m1"].Revision, s.Members["m3"].Revision))
				return original, nil
			},
			func(Snapshot, []AcceptedEvidence) ([]byte, error) { return append([]byte(nil), original...), nil },
			func(Snapshot, []AcceptedEvidence) ([]byte, error) {
				return append(append([]byte(nil), original...), ' '), nil
			},
		}
	case "noop-remove-unset":
		return []RequestBuilder{
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-same","actor":"w1","members":[{"id":"m1","expect":{"place":{"row":"r1","col":"c1"}},"move":{"row":"r1","col":"c1","score":1}}]}`, s.TableRevision)), nil
			},
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-remove","actor":"w1","members":[{"id":"m1","expect":{"revision":%q,"place":{"row":"r1","col":"c1"}},"remove":true}]}`, s.TableRevision, s.Members["m1"].Revision)), nil
			},
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-unset","actor":"w1","members":[{"id":"m1","expect":{"revision":%q,"fields":{"status":{"equals":"ready"}}},"unset":["status"]}]}`, s.TableRevision, s.Members["m1"].Revision)), nil
			},
		}
	case "create-preventive-refusal":
		var initialRev string
		return []RequestBuilder{
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				initialRev = s.TableRevision
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-create","actor":"w1","members":[{"id":"m2","expect":{"absent":true},"create":{"row":"r2","col":"c2","score":1},"set":{"status":"new"}},{"id":"m3","expect":{"revision":%q,"fields":{"token":{"equals":"permit"}}}}]}`, s.TableRevision, s.Members["m3"].Revision)), nil
			},
			func(Snapshot, []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-stale","actor":"w1","members":[{"id":"m3","expect":{"fields":{"token":{"equals":"permit"}}}}]}`, initialRev)), nil
			},
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-guard-fail","actor":"w1","members":[{"id":"m3","expect":{"fields":{"token":{"equals":"deny"}}}}]}`, s.TableRevision)), nil
			},
		}
	default:
		return nil
	}
}

// Change only a valid observed member field; keep the real Redis image,
// request, receipt and model action untouched. BatchTypeOK should remain true.
func corruptCreatedStatus(state ModelState) (ModelState, error) {
	outer := state.Values[9]
	if outer.kind != "function" || len(outer.values) != 3 {
		return state, fmt.Errorf("unexpected member-fields observation")
	}
	outer.values = append([]Expr(nil), outer.values...)
	member := outer.values[1]
	if member.kind != "function" || len(member.values) != 2 {
		return state, fmt.Errorf("unexpected m2 field observation")
	}
	member.values = append([]Expr(nil), member.values...)
	if member.values[0].kind != "string" || member.values[0].text != "new" {
		return state, fmt.Errorf("m2 status precondition differs")
	}
	member.values[0] = String("done")
	outer.values[1] = member
	state.Values[9] = outer
	return state, nil
}
