package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Codex runs one card in one headless Codex model process. The existing
// nova-friend lane loop owns take, width, generation, stop and collection;
// nova-hook owns the friend's bus delivery and challenge proof separately.
// This runner does not deliver into a desktop chat or claim idle wake.
type Codex struct {
	Stub
	Friend, Dir  string
	ConfigDir    func() string
	ModelForTier func(context.Context, string) (string, error)
	Run          Exec
	Program      string
	Out          io.Writer
}

// codexRouteModels reads the applied nova-config route projection already
// exposed by nova-sprint routes --json. This transient result is discarded
// after admission; there is no adapter model table or shadow configuration.
func codexRouteModels(raw string) (map[string]string, error) {
	var view struct {
		Routes []struct {
			Route struct {
				Name, Tier, Provider, Model, Harness string
				Enabled                              bool
				Prices                               struct{ Billing string }
			}
		}
	}
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		return nil, fmt.Errorf("Codex routes: %w", err)
	}
	models := map[string]string{}
	for _, stat := range view.Routes {
		r := stat.Route
		if !r.Enabled || r.Harness != "codex" || r.Provider != "subscription-codex" || r.Prices.Billing != "plan" || r.Model == "" || r.Tier == "" {
			continue
		}
		if _, found := models[r.Tier]; found {
			return nil, fmt.Errorf("tier %q has more than one enabled Codex subscription plan route; configure exactly one in nova-config and apply it", r.Tier)
		}
		models[r.Tier] = r.Model
	}
	return models, nil
}

// CodexRoutesReady refuses intake before the server's take when there is no
// valid Codex route at all. The per-card tier is checked again before launch.
func CodexRoutesReady(raw string) error {
	models, err := codexRouteModels(raw)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return errors.New("no enabled Codex subscription plan route; configure one in nova-config and apply it")
	}
	return nil
}

func CodexModelForTier(raw, tier string) (string, error) {
	models, err := codexRouteModels(raw)
	if err != nil {
		return "", err
	}
	if model := models[tier]; model != "" {
		return model, nil
	}
	return "", fmt.Errorf("tier %q has no enabled Codex subscription plan route; configure one in nova-config and apply it", tier)
}

func NewCodex(friend, dir string, run Exec, out io.Writer) *Codex {
	return &Codex{Stub: Stub{Harness: "codex"}, Friend: friend, Dir: dir, Run: run, Out: out}
}

func (c *Codex) program() string {
	if c.Program != "" {
		return c.Program
	}
	return "codex"
}

func (c *Codex) configDir() string {
	if c.ConfigDir == nil {
		return ""
	}
	return c.ConfigDir()
}

func (c *Codex) Refusal() string {
	dir := c.configDir()
	if filepath.IsAbs(dir) {
		return ""
	}
	return fmt.Sprintf("friend %s is a codex friend in one-shot mode without an absolute config_dir (CODEX_HOME), so no model can start; run: nova-config friend set %s --config_dir <the account's absolute Codex home>, or nova-friend run --config-dir <dir>", c.Friend, c.Friend)
}

// ProofWhy checks the account through the same wall as a card, before any
// take. A ChatGPT login is required; an API-key login cannot spend a card.
func (c *Codex) ProofWhy(ctx context.Context) string {
	if why := c.Refusal(); why != "" {
		return why
	}
	// codex login status writes its answer to stderr. The Exec interface
	// captures stdout, so a fixed shell wrapper joins that one answer without
	// interpolating account paths or any credentials into shell source.
	args := append([]string{"-c", `exec "$@" 2>&1`, "sh", "env"}, c.accountEnv()...)
	args = append(args, c.program(), "-c", "forced_login_method=chatgpt", "login", "status")
	out, exit, err := c.Run(LaneContext(ctx), c.Dir, "sh", args, "")
	if err != nil || exit != 0 || strings.TrimSpace(out) != "Logged in using ChatGPT" {
		return "harness codex cannot answer with a ChatGPT subscription in its lane wall; sign in with ChatGPT in its config_dir"
	}
	return ""
}

func (c *Codex) accountEnv() []string {
	return []string{"-u", "CODEX_API_KEY", "-u", "OPENAI_API_KEY", "CODEX_HOME=" + c.configDir()}
}

func (c *Codex) run(ctx context.Context, dir, prompt, model, extraWrite string, noGit bool) (LaneTurn, string, error) {
	if !InLane(ctx) {
		return LaneTurn{}, "", errors.New("a Codex model cannot start outside a nova-friend lane wall")
	}
	if why := c.Refusal(); why != "" {
		return LaneTurn{}, "", errors.New(why)
	}
	// The validated nova-friend wall is the OS boundary. A second Codex
	// Seatbelt inside it cannot inspect denied roots and fails to write the
	// card's outbox. This CLI mode is intended for externally sandboxed runs;
	// main.go refuses to construct this runner without its wall.
	args := append(c.accountEnv(), c.program(), "-c", "forced_login_method=chatgpt", "--dangerously-bypass-approvals-and-sandbox", "exec", "--ignore-user-config", "--json", "--cd", dir)
	if noGit {
		args = append(args, "--skip-git-repo-check")
	}
	if extraWrite != "" {
		args = append(args, "--add-dir", extraWrite)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "-")
	out, exit, err := c.Run(ctx, dir, "env", args, prompt)
	if c.Out != nil && out != "" {
		fmt.Fprintln(c.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	exit, err = refused("codex exec", out, exit, err)
	return LaneTurn{Exit: exit, Rejected: PermissionRejection(out), FirstError: HarnessFirstError(out)}, out, err
}

func (c *Codex) RunCard(ctx context.Context, card Card) (LaneTurn, error) {
	if !InLane(ctx) {
		return LaneTurn{}, errors.New("a Codex card cannot start outside a nova-friend lane wall")
	}
	model := card.Model
	if model == "" {
		return LaneTurn{}, fmt.Errorf("card %s has no configured Codex route for tier %q; no model started", card.ID, card.Tier)
	}
	brief, err := os.ReadFile(card.Brief)
	if err != nil {
		return LaneTurn{}, fmt.Errorf("the card's brief: %w", err)
	}
	job := LaneDirOf(ctx, c.Dir)
	repo := filepath.Join(job, "repo")
	if _, statErr := os.Stat(filepath.Join(repo, ".git")); statErr != nil {
		// nova-friend stages jobs/<job>/repo; nova-sprint friend stage writes
		// the checkout directly at jobs/<job>. Both have a JOB.md beside the
		// checkout and are real supported layouts.
		repo = job
	}
	if fi, statErr := os.Stat(repo); statErr != nil || !fi.IsDir() {
		return LaneTurn{}, fmt.Errorf("the card's staged repo %s is missing; stage this card before starting a Codex model", repo)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".git")); statErr != nil {
		return LaneTurn{}, fmt.Errorf("the card's staged repo %s has no .git; stage this card before starting a Codex model: %w", repo, statErr)
	}
	if err := os.MkdirAll(card.Outbox, 0o755); err != nil {
		return LaneTurn{}, fmt.Errorf("the card's outbox: %w", err)
	}
	turn, _, err := c.run(ctx, repo, string(brief), model, card.Outbox, false)
	if err != nil {
		return turn, err
	}
	var missing []string
	for _, name := range []string{"REPORT.md", "RESULT.md"} {
		if !exists(filepath.Join(card.Outbox, name)) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return turn, NoReport{Run: "codex exec", Exit: turn.Exit, Outbox: card.Outbox, Lacks: missing}
	}
	return turn, nil
}

func (c *Codex) RunRead(ctx context.Context, model, prompt string) (LaneTurn, error) {
	if !InLane(ctx) {
		return LaneTurn{}, errors.New("a Codex read cannot start outside a nova-friend lane wall")
	}
	if model == "" {
		return LaneTurn{}, errors.New("the read has no supported Codex tier; no model started")
	}
	turn, _, err := c.run(ctx, c.Dir, prompt, model, "", true)
	return turn, err
}
