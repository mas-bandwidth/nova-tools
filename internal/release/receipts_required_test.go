package release

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

// Count every forge method, including reads that do not reach HeadSHA, while
// retaining the existing successful cut fixture for the waiver rows.
type receiptsForge struct {
	Forge
	calls []string
}

func (f *receiptsForge) HeadSHA(ctx context.Context, repo, branch string) (string, error) {
	f.calls = append(f.calls, "HeadSHA")
	return f.Forge.HeadSHA(ctx, repo, branch)
}

func (f *receiptsForge) CheckRuns(ctx context.Context, repo, sha string) ([]CheckRun, error) {
	f.calls = append(f.calls, "CheckRuns")
	return f.Forge.CheckRuns(ctx, repo, sha)
}

func (f *receiptsForge) Tags(ctx context.Context, repo string) ([]string, error) {
	f.calls = append(f.calls, "Tags")
	return f.Forge.Tags(ctx, repo)
}

func (f *receiptsForge) Compare(ctx context.Context, repo, base, head string) ([]Commit, error) {
	f.calls = append(f.calls, "Compare")
	return f.Forge.Compare(ctx, repo, base, head)
}

func (f *receiptsForge) Files(ctx context.Context, repo, base, head string) ([]string, error) {
	f.calls = append(f.calls, "Files")
	return f.Forge.Files(ctx, repo, base, head)
}

func (f *receiptsForge) Tag(ctx context.Context, repo, tag, sha, message string) error {
	f.calls = append(f.calls, "Tag")
	return f.Forge.Tag(ctx, repo, tag, sha, message)
}

func (f *receiptsForge) TagMessage(ctx context.Context, repo, tag string) (string, error) {
	f.calls = append(f.calls, "TagMessage")
	return f.Forge.TagMessage(ctx, repo, tag)
}

// fakeToolchain already counts builds; also count its platform query so a
// refusal cannot reach even the toolchain's read before it refuses.
type receiptsToolchain struct {
	fakeToolchain
	platformCalls int
}

func (tc *receiptsToolchain) Platforms(ctx context.Context) ([]string, error) {
	tc.platformCalls++
	return tc.fakeToolchain.Platforms(ctx)
}

// SPEC-RELEASE: a resolved command reference requires explicit receipts or a
// reasoned waiver, before cut reads the forge or build asks the toolchain.
func TestReleaseRequiresReceiptsForAResolvedReference(t *testing.T) {
	t.Parallel()

	const why = "receipt recording is unavailable during the tracked maintenance"
	for _, verb := range []string{"cut", "build"} {
		for _, reference := range []string{"explicit", "derived"} {
			for _, policy := range []struct {
				name string
				args []string
				code int
			}{
				{"missing receipts", nil, 2},
				{"waiver without reason", []string{"--no-dogfood-gate"}, 2},
				{"reasoned waiver", []string{"--no-dogfood-gate", "--reason", why}, 0},
			} {
				t.Run(verb+"/"+reference+"/"+policy.name, func(t *testing.T) {
					t.Parallel()

					root := sourceTree(t)
					changelog := changelogIn(t, root)
					outDir := filepath.Join(root, "release")
					require.NoError(t, os.MkdirAll(outDir, 0o755))
					cliDir := filepath.Join(root, "reference")
					if reference == "derived" {
						cliDir = filepath.Join(root, "docs")
					}
					require.NoError(t, os.MkdirAll(cliDir, 0o755))
					cli := dogfoodCLIFile(t, cliDir)
					args := cutArgs(changelog)
					if verb == "build" {
						args = []string{"build", "--version", "v0.16.0", "--source", root,
							"--out", outDir, "--platform", "linux-amd64"}
					}
					if reference == "explicit" {
						args = append(args, "--cli", cli)
					}
					args = append(args, policy.args...)
					forge := cutForge()
					traced := &receiptsForge{Forge: forge}
					tc := &receiptsToolchain{}
					deps := cutDeps(t, traced)
					deps.Toolchain = tc
					gateCalls := 0
					deps.Dogfood = func(_, _, _ string) (DogfoodVerdict, error) {
						gateCalls++
						return DogfoodVerdict{}, fmt.Errorf("the gate must not read absent receipts or a waived input")
					}
					before := testkit.Snapshot(t, root)
					var out, errs bytes.Buffer
					code := Run("nova-update", args, &out, &errs, deps)

					assert.Equal(t, policy.code, code, "stdout:\n%s\nstderr:\n%s", &out, &errs)
					assert.Zero(t, gateCalls, "missing receipts and explicit waivers precede gate reads")
					assert.NotContains(t, out.String()+errs.String(), "dogfood=skipped")
					assert.NotContains(t, out.String()+errs.String(), "dogfood-gate=skipped")
					if policy.code == 2 {
						assert.Contains(t, errs.String(), strings.ToUpper(verb)+" REFUSED")
						assert.Contains(t, errs.String(), "--no-dogfood-gate")
						assert.Contains(t, errs.String(), "--reason")
						if policy.name == "missing receipts" {
							assert.Contains(t, errs.String(), "--receipts")
						}
						assert.Empty(t, traced.calls, "refusal must precede every forge method")
						assert.Empty(t, forge.tagged)
						assert.Zero(t, tc.platformCalls)
						assert.Empty(t, tc.calls)
						assert.Equal(t, before, testkit.Snapshot(t, root), "refusal changed files")
						return
					}

					assert.Contains(t, out.String(), "DOGFOOD WAIVED reason=")
					assert.Contains(t, out.String(), "dogfood=waived")
					assert.Contains(t, out.String(), field(why))
					if verb == "cut" {
						assert.Len(t, forge.tagged, 1)
						assert.Contains(t, traced.calls, "Tag")
						assert.Contains(t, testkit.ReadFile(t, changelog), DogfoodWaiverPrefix+why)
						assert.Zero(t, tc.platformCalls)
						assert.Empty(t, tc.calls)
					} else {
						assert.Empty(t, traced.calls)
						assert.Equal(t, 1, tc.platformCalls)
						assert.Len(t, tc.calls, 3)
						assert.NotEmpty(t, testkit.ReadFile(t, filepath.Join(outDir, "v0.16.0", "linux-amd64", SumsFile)))
					}
				})
			}
		}
	}
}
