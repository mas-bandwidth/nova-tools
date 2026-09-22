package pulse

import (
	"strings"
	"testing"
)

// TestCommitDecision is ONE ROW PER RULE of the commit step (nova-tools #2549). The bash it
// replaces had no test at all, which is why the same rule broke three times in twelve
// hours: the whole step was a single-quoted string inside an ssh, and the only way to try a
// change was to run it on the fleet.
//
// The row NAMES are the `reason=` tokens the verb prints, so a line in a bench log leads
// straight to the row that pins it.
func TestCommitDecision(t *testing.T) {
	const done = "DONE (both runs green)"
	goFile := ChangedFile{Path: "internal/pulse/harvestcommit.go", Size: 4096}

	cases := []struct {
		name   string
		job    CommitJob
		action CommitAction
		why    string
		check  func(t *testing.T, p CommitPlan)
	}{{
		// R1. The rule the bash got wrong twice. Its test stripped the whitespace
		// out of line 2 and compared to `DONE`, so `DONE (both runs green)` never
		// matched; the `case ... DONE\(*` written to fix that did not parse on the
		// remote bash and stopped every bench for seven hours.
		name:   "not-done/a verdict that is not DONE",
		job:    CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: "ABSTAIN out-of-scope"},
		action: CommitSkip, why: CommitWhyNotDone,
	}, {
		name:   "not-done/DONEish is not DONE",
		job:    CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: "DONEish"},
		action: CommitSkip, why: CommitWhyNotDone,
	}, {
		name: "work/DONE with a suffix IS DONE",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true,
			Line1: "RESULT fix-1 sha=abcdef1234 -- the work", Line2: done,
			Changed: []ChangedFile{goFile}},
		action: CommitCommit, why: CommitWhyWorkIsCommit,
		check: func(t *testing.T, p CommitPlan) {
			if p.Branch != "rowan/fix-1" {
				t.Errorf("branch = %q, want rowan/fix-1", p.Branch)
			}
			if p.Message != "RESULT fix-1 sha=abcdef1234 -- the work" {
				t.Errorf("message = %q, want RESULT.md line 1", p.Message)
			}
			if p.BaseSHA != "abcdef1234" {
				t.Errorf("BaseSHA = %q, want abcdef1234 off line 1", p.BaseSHA)
			}
		},
	}, {
		// R2.
		name:   "read-card/a read card commits nothing",
		job:    CommitJob{Label: "card-00-read-nova-tools-2537", HasResult: true, HasRepo: true, Line2: done},
		action: CommitSkip, why: CommitWhyReadCard,
	}, {
		// R3. From a FILE now, not nine prefixes inside a shell string inside an ssh.
		name: "draft-only/a friend-owned tooling card is a draft",
		job: CommitJob{Label: "card-00-fix-nova-tools-2454-r1", HasResult: true, HasRepo: true, Line2: done,
			DraftOnly: []string{"# friends build the tooling", "fix-nova-tools-2454-*"}},
		action: CommitSkip, why: CommitWhyDraftOnly,
	}, {
		// R4.
		name:   "no-repo/the job holds no git repository",
		job:    CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: false, Line2: done},
		action: CommitSkip, why: CommitWhyNoRepo,
	}, {
		// R5. A crash is named, not silently counted as a skip.
		name:   "no-result/the job wrote no RESULT.md",
		job:    CommitJob{Label: "card-00-fix-1", HasRepo: true},
		action: CommitSkip, why: CommitWhyNoResult,
	}, {
		// R6. The exfiltration fence's first half (#2609).
		name: "outside-tree/a changed path leaves the repository",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			Changed: []ChangedFile{goFile, {Path: "../../.ssh/id_ed25519", Size: 400}}},
		action: CommitRefuse, why: CommitWhyOutsideTree,
		check: func(t *testing.T, p CommitPlan) {
			if p.Path != "../../.ssh/id_ed25519" || p.Shape != "outside-tree" {
				t.Errorf("path/shape = %q/%q, want the offending path named", p.Path, p.Shape)
			}
		},
	}, {
		// R7. The second half: a path with a credential's SHAPE.
		name: "secret-path/a changed path is shaped like a key",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			Changed: []ChangedFile{goFile, {Path: "infra/deploy.key", Size: 1700}}},
		action: CommitRefuse, why: CommitWhySecretPath,
		check: func(t *testing.T, p CommitPlan) {
			if p.Path != "infra/deploy.key" || p.Shape != "key-file" {
				t.Errorf("path/shape = %q/%q, want infra/deploy.key/key-file", p.Path, p.Shape)
			}
		},
	}, {
		// R8.
		name: "too-big/one changed file over 1 MB refuses the job by name",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			Changed: []ChangedFile{goFile, {Path: "testdata/dump.bin", Size: commitMaxFileBytes + 1}}},
		action: CommitRefuse, why: CommitWhyTooBig,
		check: func(t *testing.T, p CommitPlan) {
			if p.Path != "testdata/dump.bin" {
				t.Errorf("path = %q, want the file named", p.Path)
			}
			if !strings.Contains(p.Detail, "left uncommitted") {
				t.Errorf("detail = %q, want it to say the work is left uncommitted", p.Detail)
			}
		},
	}, {
		// R9. Set aside AND commit: refusing the whole job over one of these left 88
		// DONE cells unharvested on 2026-09-21.
		name: "work/the card's own files are set aside, not committed",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			Changed: []ChangedFile{goFile, {Path: "RESULT.md", Size: 900}, {Path: "usage.tsv", Size: 240}}},
		action: CommitCommit, why: CommitWhyWorkIsCommit,
		check: func(t *testing.T, p CommitPlan) {
			if got := strings.Join(p.SetAside, ","); got != "RESULT.md,usage.tsv" {
				t.Errorf("SetAside = %q, want RESULT.md,usage.tsv", got)
			}
		},
	}, {
		// R10. A4 (#2522): with a card, its declared PATHS and nothing else.
		name: "work/a card stages its declared PATHS and nothing else",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			Changed:   []ChangedFile{goFile},
			CardFound: true, CardPaths: []string{"internal/pulse/harvestcommit.go"}},
		action: CommitCommit, why: CommitWhyWorkIsCommit,
		check: func(t *testing.T, p CommitPlan) {
			if p.StageAll {
				t.Error("StageAll is set although the card declared PATHS")
			}
			if got := strings.Join(p.Stage, ","); got != "internal/pulse/harvestcommit.go" {
				t.Errorf("Stage = %q, want the card's PATHS", got)
			}
			if p.Warn != "" {
				t.Errorf("Warn = %q, want none when a card was found", p.Warn)
			}
		},
	}, {
		// R11. No card is a WARN and not a refusal: refusing here is how DONE work
		// goes unharvested, and a silent `-A` is how A4 was broken all along.
		name: "work/no card stages the whole tree and says so",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			Changed: []ChangedFile{goFile}},
		action: CommitCommit, why: CommitWhyWorkIsCommit,
		check: func(t *testing.T, p CommitPlan) {
			if !p.StageAll || p.Warn == "" {
				t.Errorf("StageAll=%v Warn=%q, want the whole tree staged with a printed WARN", p.StageAll, p.Warn)
			}
		},
	}, {
		// R12.
		name: "nothing-to-do/a clean tree only needs its branch",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			Line1: "RESULT fix-1 -- the work"},
		action: CommitBranchOnly, why: CommitWhyNothingToDo,
	}, {
		// R13.
		name: "work/a BRANCH line off the prefix is put under it",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			ResultBranch: "spec/fix-1", HasBranchLine: true, Changed: []ChangedFile{goFile}},
		action: CommitCommit, why: CommitWhyWorkIsCommit,
		check: func(t *testing.T, p CommitPlan) {
			if p.Branch != "rowan/spec/fix-1" {
				t.Errorf("branch = %q, want rowan/spec/fix-1", p.Branch)
			}
		},
	}, {
		// R14. THE BRANCH LINE THAT NEVER TOOK (#2549). A report-* card had no BRANCH
		// line at all, and `sed "s#^BRANCH[: ].*#...#"` substitutes nothing when the
		// line is absent and exits 0: the verdict was printed 94 times and never took.
		name: "work/an absent BRANCH line is WRITTEN, not substituted",
		job: CommitJob{Label: "card-00-report-nova-tools-2537-r1", HasResult: true, HasRepo: true,
			Line2: done, Changed: []ChangedFile{goFile}},
		action: CommitCommit, why: CommitWhyWorkIsCommit,
		check: func(t *testing.T, p CommitPlan) {
			if !p.WriteBranch {
				t.Error("WriteBranch is false although the RESULT.md has no BRANCH line")
			}
			if p.Base != "" {
				t.Errorf("Base = %q, want none: a report card is rebased onto nothing", p.Base)
			}
		},
	}, {
		// R15.
		name: "work/a recut card carries the PR it recuts",
		job: CommitJob{Label: "card-00-recut-nova-tools-2498-r2", HasResult: true, HasRepo: true,
			Line2: done, Changed: []ChangedFile{goFile}},
		action: CommitCommit, why: CommitWhyWorkIsCommit,
		check: func(t *testing.T, p CommitPlan) {
			if p.PriorPR != 2498 || p.PriorRepo != "nova-tools" || !p.WritePrior {
				t.Errorf("prior = %s#%d write=%v, want nova-tools#2498 written", p.PriorRepo, p.PriorPR, p.WritePrior)
			}
		},
	}, {
		// R16. The card's BASE beats the RESULT.md's: one is what the pulse cut, the
		// other is what a worker wrote, and only one of the two is evidence.
		name: "work/the card's BASE wins and the RESULT line is written to match",
		job: CommitJob{Label: "card-00-fix-1", HasResult: true, HasRepo: true, Line2: done,
			Changed: []ChangedFile{goFile}, CardFound: true, CardBase: "fixed-table-form",
			ResultBase: "dev", HasBaseLine: true, FallbackBase: "dev"},
		action: CommitCommit, why: CommitWhyWorkIsCommit,
		check: func(t *testing.T, p CommitPlan) {
			if p.Base != "fixed-table-form" || !p.WriteBase {
				t.Errorf("base = %q write=%v, want fixed-table-form written over the RESULT's dev", p.Base, p.WriteBase)
			}
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := CommitDecision(tc.job)
			if p.Action != tc.action || p.Why != tc.why {
				t.Fatalf("action/why = %s/%s, want %s/%s (detail: %s)", p.Action, p.Why, tc.action, tc.why, p.Detail)
			}
			if p.Why == "" {
				t.Error("every plan names a reason; this one names none")
			}
			if strings.HasPrefix(p.Label, "card-00-") {
				t.Errorf("label = %q, want the cutter's card-00- prefix stripped", p.Label)
			}
			if tc.check != nil {
				tc.check(t, p)
			}
		})
	}
}

// TestCommitDoneIsAWord pins R1 on its own, because it is the rule that cost seven hours of
// the fleet: `DONE` is the first WORD of line 2 and not a substring of it.
func TestCommitDoneIsAWord(t *testing.T) {
	for _, s := range []string{"DONE", "DONE (both runs green)", " DONE ", "DONE -- green", "DONE(green)"} {
		if !commitLineIsDone(s) {
			t.Errorf("%q is DONE and was read as not DONE", s)
		}
	}
	for _, s := range []string{"DONEish", "DONE_LATER", "NEARLY DONE", "ABSTAIN", "BLOCKED head=x", "", "done"} {
		if commitLineIsDone(s) {
			t.Errorf("%q is not DONE and was read as DONE", s)
		}
	}
}

// TestCommitSecretPathShapes walks #2609's own list: every shape in the issue is recognised
// and named, and an ordinary repository path is not.
func TestCommitSecretPathShapes(t *testing.T) {
	want := map[string]string{
		".ssh/id_rsa":                      "ssh-dir",
		"infra/id_ed25519":                 "ssh-private-key",
		"deploy.key":                       "key-file",
		"certs/server.pem":                 "pem-file",
		"auth.json":                        "auth-file",
		".env":                             "dotenv",
		".env.production":                  "dotenv",
		"nova-secrets/x.age":               "secrets-dir",
		"infra/nova-bench/secrets/key.txt": "secrets-dir",
	}
	for p, shape := range want {
		if got := secretPathShape(p); got != shape {
			t.Errorf("secretPathShape(%q) = %q, want %q", p, got, shape)
		}
	}
	for _, p := range []string{"internal/pulse/harvestcommit.go", "docs/CLI.md", "testdata/id_map.json.pub", "README.md"} {
		if got := secretPathShape(p); got != "" {
			t.Errorf("secretPathShape(%q) = %q, want no shape", p, got)
		}
	}
	for _, p := range []string{"/etc/passwd", "../outside.txt", "a/../../b", "~/.ssh/config"} {
		if !outsideRepoTree(p) {
			t.Errorf("outsideRepoTree(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"internal/pulse/x.go", "a/b/c.txt", "./docs/CLI.md"} {
		if outsideRepoTree(p) {
			t.Errorf("outsideRepoTree(%q) = true, want false", p)
		}
	}
}

// TestCommitBranchNameStaysUnderThePrefix: the branch is held against the same rule every
// push in this package is held against (Stella's ruling on #1824).
func TestCommitBranchNameStaysUnderThePrefix(t *testing.T) {
	for _, tc := range []struct{ label, result, want string }{
		{"fix-1", "", "rowan/fix-1"},
		{"fix-1", "rowan/other", "rowan/other"},
		{"fix-1", "other", "rowan/other"},
	} {
		got, err := commitBranchName(tc.label, tc.result)
		if err != nil || got != tc.want {
			t.Errorf("commitBranchName(%q,%q) = %q,%v; want %q", tc.label, tc.result, got, err, tc.want)
		}
	}
	for _, bad := range []string{"main", "master"} {
		if _, err := commitBranchName("x", bad); err == nil {
			t.Errorf("commitBranchName with BRANCH %q was allowed; harvest never commits a trunk", bad)
		}
	}
	if _, err := commitBranchName("x", "rowan/a b"); err == nil {
		t.Error("a branch name with a space was allowed")
	}
}

// TestCommitMessageIsLineOneCapped: the subject is RESULT.md line 1, on one line, capped.
func TestCommitMessageIsLineOneCapped(t *testing.T) {
	long := "RESULT x " + strings.Repeat("a", 400)
	if got := commitMessage(long); len(got) > commitMessageMax {
		t.Errorf("message is %d bytes, over the %d cap", len(got), commitMessageMax)
	}
	if got := commitMessage("RESULT x\nsecond line"); strings.Contains(got, "\n") {
		t.Errorf("message = %q, want one line", got)
	}
	if got := commitMessage("   "); got == "" {
		t.Error("an empty line 1 must still produce a commit subject")
	}
}
