package card_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// wrapperKeyRE is the DONE-WHEN's list of keys no prompt may name.
var wrapperKeyRE = regexp.MustCompile(`\b(SCHEMA|ATTEMPT|CHECK|RED|GREEN|BRANCH)\b`)

// promptIssue quotes wrapper keys on purpose: the issue is data, and only the
// prompt around it is held to the rule.
const promptIssue = "STREAM: swarm: cards\nPATHS: internal/x/x.go\n\nBuild x. The old card asked for SCHEMA, BRANCH and GREEN.\n\nDONE-WHEN: `go test ./internal/x -run TestX` passes."

// TestCardPromptPerFamily is the DONE-WHEN control of nova-tools#3956.
//
//   - render: for every family (and default), card render --id <task>
//     --for-model <family> (taskcard.RenderPrompt over a task record in
//     Redis) prints the goal on the first line, no wrapper key before the
//     issue, a literal RESULT.md example, and the issue text last, under
//     2,500 bytes without it; a cfg:card:template:<family> override renders
//     in place of the built-in one, and one that breaks the shape is refused.
//   - families: every family is some routes.yaml model's.
//   - no-result: a model that commits on the card branch and writes no
//     RESULT.md (harness exit 1, kimi-k3's swarm-0925a shape) ends DONE with
//     the commit as pushed_sha and w_commit_sha, w_line2 `DONE (no
//     RESULT.md, commit <sha8>)`, a valid record and the check run; with no
//     commit it stays FAILED crash.
func TestCardPromptPerFamily(t *testing.T) {
	t.Run("render", func(t *testing.T) {
		ctx := context.Background()
		_, client := newSprint(t)
		sha := strings.Repeat("ab", 20)
		if err := client.HSet(ctx, taskcard.Key("c1"), map[string]string{
			"route": "pro", "kind": "build", "repo": "mas-bandwidth/nova-tools", "base": "dev", "base_sha": sha,
			"paths": "internal/x/x.go internal/x/x_test.go", "task": "Build x so the card test passes.",
			"done_when": "`go test ./internal/x -run TestX` passes.", "body": promptIssue, "stream": "swarm: cards",
		}).Err(); err != nil {
			t.Fatal(err)
		}
		texts := map[string]string{}
		for _, fam := range append(append([]string{}, card.Families...), card.FamilyDefault) {
			p, err := taskcard.RenderPrompt(ctx, client, "c1", fam)
			if err != nil {
				t.Fatalf("%s: %v", fam, err)
			}
			text := string(p.Text)
			texts[fam] = text
			at := strings.LastIndex(text, promptIssue)
			if at < 0 {
				t.Fatalf("%s: the issue text is not in the prompt:\n%s", fam, text)
			}
			head, tail := text[:at], text[at+len(promptIssue):]
			if strings.TrimSpace(tail) != "" && !regexp.MustCompile(`^\s*</[a-z_]+>\s*$`).MatchString(tail) {
				t.Errorf("%s: text after the issue %q; the issue is last", fam, tail)
			}
			if k := wrapperKeyRE.FindString(head); k != "" {
				t.Errorf("%s: the prompt names wrapper key %s:\n%s", fam, k, head)
			}
			if first, _, _ := strings.Cut(head, "\n"); !strings.Contains(first, "Build x so the card test passes.") {
				t.Errorf("%s: first line %q, want the goal", fam, first)
			}
			if !strings.Contains(head, "RESULT: c1 sha="+sha[:12]+"\nDONE\n") {
				t.Errorf("%s: no literal RESULT.md example:\n%s", fam, head)
			}
			for _, want := range []string{"internal/x/x.go internal/x/x_test.go", "go test ./internal/x -run '^TestX$' -count=1", "TestX", "ABSTAIN", "BLOCKED"} {
				if !strings.Contains(head, want) {
					t.Errorf("%s: the prompt lacks %q", fam, want)
				}
			}
			if n := len(text) - len(promptIssue); n >= card.PromptBudget || p.PromptBytes >= card.PromptBudget {
				t.Errorf("%s: prompt %d bytes (reported %d) without the issue, want under %d", fam, n, p.PromptBytes, card.PromptBudget)
			}
			if p.Family != fam || p.Source != "builtin" {
				t.Errorf("%s: family=%s source=%s, want builtin", fam, p.Family, p.Source)
			}
		}
		// One framing per family: qwen shares the default's; the other five
		// are their own.
		seen := map[string]string{}
		for _, fam := range card.Families {
			if other, dup := seen[texts[fam]]; dup {
				t.Errorf("%s renders the same prompt as %s", fam, other)
			}
			seen[texts[fam]] = fam
		}
		// A model launch string reads its family's template.
		if p, err := taskcard.RenderPrompt(ctx, client, "c1", "moonshotai/kimi-k3"); err != nil || p.Family != card.FamilyKimi || string(p.Text) != texts[card.FamilyKimi] {
			t.Errorf("--for-model moonshotai/kimi-k3 = %s %v, want the kimi prompt", p.Family, err)
		}

		// A Redis override renders in place of the built-in template.
		good := "Goal: {{.Goal}}\nFiles: {{.Paths}}\nTest: {{.TestCmd}}\nDone when: {{.DoneWhen}}\nWrite RESULT.md:\n{{.Contract}}\nDONE\nor ABSTAIN <why> or BLOCKED <why>.\nNo push, no PR, no subagent, no question.\n<issue>\n{{.Issue}}\n</issue>\n"
		if err := client.Set(ctx, card.TemplateKey(card.FamilyKimi), good, 0).Err(); err != nil {
			t.Fatal(err)
		}
		p, err := taskcard.RenderPrompt(ctx, client, "c1", "kimi")
		if err != nil || p.Source != "redis" || !strings.HasPrefix(string(p.Text), "Goal: Build x") || !strings.HasSuffix(string(p.Text), promptIssue+"\n</issue>\n") {
			t.Fatalf("redis template = %v %s %q", err, p.Source, p.Text)
		}
		// An override that breaks the shape is refused, naming the rule.
		for name, bad := range map[string]string{
			"wrapper key": strings.Replace(good, "Files:", "BRANCH: rowan/x\nFiles:", 1),
			"issue last":  strings.Replace(good, "</issue>\n", "</issue>\nAnd one more thing: write a long report after all of this.\n", 1),
			"no example":  strings.Replace(good, "{{.Contract}}\nDONE\n", "", 1),
			"goal first":  "Files: {{.Paths}}\n" + strings.Replace(good, "Goal: {{.Goal}}\n", "", 1),
			"budget":      strings.Replace(good, "Files:", strings.Repeat("Be careful. ", 230)+"\nFiles:", 1),
			"no issue":    strings.Replace(good, "{{.Issue}}", "", 1),
		} {
			if err := client.Set(ctx, card.TemplateKey(card.FamilyGLM), bad, 0).Err(); err != nil {
				t.Fatal(err)
			}
			_, err := taskcard.RenderPrompt(ctx, client, "c1", "glm")
			if why, ok := taskcard.IsRefused(err); !ok || !strings.HasPrefix(why, "PROMPT template glm") {
				t.Errorf("%s: override rendered, err=%v", name, err)
			}
		}
		if _, err := taskcard.RenderPrompt(ctx, client, "nope", "qwen"); err == nil || !strings.Contains(err.Error(), "NOTASK") {
			t.Errorf("render of no record = %v, want NOTASK", err)
		}
	})

	t.Run("families", func(t *testing.T) {
		tab, err := route.Load()
		if err != nil {
			t.Fatal(err)
		}
		reached := map[string][]string{}
		for _, r := range tab.Rows() {
			f := card.FamilyOf(r.Launch())
			reached[f] = append(reached[f], r.Model)
		}
		for _, f := range card.Families {
			if len(reached[f]) == 0 {
				t.Errorf("family %s is no routes.yaml model's", f)
			}
			if card.FamilyOf(f) != f {
				t.Errorf("FamilyOf(%s) = %s", f, card.FamilyOf(f))
			}
		}
		for model, want := range map[string]string{"moonshotai/kimi-k3": "kimi", "deepseek-v4-pro": "deepseek", "opencode/qwen3.6-plus": "qwen",
			"z-ai/glm-5.3": "glm", "inception/mercury-2.5": "mercury", "anthropic/claude-haiku-4.5": "claude", "xiaomi/mimo-v2.5": "default"} {
			if got := card.FamilyOf(model); got != want {
				t.Errorf("FamilyOf(%s) = %s, want %s", model, got, want)
			}
		}
	})

	t.Run("no-result", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git unavailable")
		}
		if _, err := exec.LookPath("go"); err != nil {
			t.Skip("go unavailable")
		}
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			mode string
			done bool
		}{{"native-noresult", true}, {"native-noresult-nocommit", false}} {
			t.Run(tc.mode, func(t *testing.T) {
				ctx := context.Background()
				st, client := newSprint(t)
				origin, base := newGoOrigin(t)
				id := card.Identity{Sprint: "swarm-0925a", Label: "kimi-" + tc.mode, BaseSHA: base[:8], Bench: "wrap-bench", Attempt: 1}
				token := attemptToken(1, strings.Repeat("f", 32))
				seedCard(t, ctx, client, id, "dealt", token)
				if err := client.HSet(ctx, card.CardKey(id.Sprint, id.Label), map[string]string{
					"kind": typedrec.KindFix, "repo": "mas-bandwidth/nova-tools", "base_sha": base, "test": ". TestPass",
				}).Err(); err != nil {
					t.Fatal(err)
				}
				t.Setenv(fakeHarnessEnv, tc.mode)
				t.Setenv(fakeOriginEnv, origin)
				t.Setenv(fakeSlotEnv, filepath.Join(t.TempDir(), "slot"))
				h := newHarnessRun(t, id, self)
				rep := card.RunWrapper(ctx, h.cfg, &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token})
				hs := hashOf(t, ctx, client, id.Sprint, id.Label)
				res, err := client.HGetAll(ctx, card.CardKey(id.Sprint, id.Label)+":result:a1").Result()
				if err != nil {
					t.Fatal(err)
				}
				if !tc.done {
					if rep.Outcome != "FAILED" || rep.Reason != "crash" || hs["outcome"] != "FAILED" || hs["where"] != "done" || hs["where_ok"] != "fail" {
						t.Fatalf("no commit, no RESULT.md: report %s, hash %v; want FAILED crash as before", rep.Line(), hs)
					}
					if res["w_result"] != "absent" {
						t.Fatalf("w_result %q, want absent", res["w_result"])
					}
					return
				}
				if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" || rep.Reason != "done" {
					t.Fatalf("report %s why=%q; want ENDED DONE done", rep.Line(), rep.Why)
				}
				if hs["state"] != "ended" || hs["outcome"] != "DONE" || hs["where"] != "done" || hs["where_ok"] != "ok" || len(hs["pushed_sha"]) != 40 {
					t.Fatalf("card hash %v, want ended DONE in done/ok with a pushed_sha", hs)
				}
				model := res["w_commit_sha"]
				if len(model) != 40 || hs["pushed_sha"] != model {
					t.Fatalf("w_commit_sha %q pushed_sha %q, want the model's commit as both", model, hs["pushed_sha"])
				}
				for k, v := range map[string]string{
					"valid": "1", "w_result": "synthesized", "w_line2": "DONE (no RESULT.md, commit " + model[:8] + ")",
					"w_outcome": "DONE", "w_reason": "done", "w_exit": "1", "w_commit": "COMMITTED", "c_paths": "base.txt", "c_check": "pass",
				} {
					if res[k] != v {
						t.Errorf("result.%s = %q, want %q", k, res[k], v)
					}
				}
				if !strings.Contains(hs["why"], "no RESULT.md") {
					t.Errorf("card why %q, want the fallback named", hs["why"])
				}
				h.assertNoJobDir()
			})
		}
	})
}
