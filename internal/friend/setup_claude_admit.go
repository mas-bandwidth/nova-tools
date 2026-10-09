package friend

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

const claudeAccountsOwnedName = "CLAUDE_ACCOUNTS.tsv"

var claudeAccountsDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ClaudeAccountsOwnedPath is the private, public-metadata copy kept with one
// selected setup profile. It contains no token value or harness auth file.
func ClaudeAccountsOwnedPath(configDir string) string {
	return filepath.Join(configDir, ".nova-friend-setup", claudeAccountsOwnedName)
}

func hasClaudeAdmissionSelection(o SetupOptions) bool {
	return o.ClaudeAccountID != "" || o.ClaudeMachine != "" || o.ClaudeAccountsPath != "" || o.ClaudeAccountsSHA256 != "" || o.ClaudeAccountsSource != ""
}

func claudeTokenManifestSelection(o SetupOptions, raw []byte) error {
	if len(raw) > 64<<10 || !claudeAccountsDigest.MatchString(o.ClaudeAccountsSHA256) || secrets.ClaudeTokenFleetSHA256(raw) != o.ClaudeAccountsSHA256 {
		return errors.New("Claude account manifest bytes differ from the pinned gate digest; use the reviewed manifest and rerun explicit setup")
	}
	manifest, err := secrets.ParseClaudeTokenFleetManifest(raw)
	if err != nil {
		return fmt.Errorf("Claude account manifest is invalid: %w", err)
	}
	for _, account := range manifest.Accounts {
		if account.ID != o.ClaudeAccountID {
			continue
		}
		if account.SourceMachine == "" || account.SourceSeat == "" {
			break
		}
		for _, target := range account.Targets {
			if target.Machine == o.ClaudeMachine && target.Profile == o.Profile && target.Seat == o.Seat {
				return nil
			}
		}
	}
	return errors.New("Claude account manifest has no exact account, machine, profile and seat binding for this setup; select the reviewed tuple")
}

func claudeOwnedAdmission(o SetupOptions, fs SettingsFS) error {
	if o.ClaudeAccountsPath != ClaudeAccountsOwnedPath(o.ConfigDir) || !claudeAccountsDigest.MatchString(o.ClaudeAccountsSHA256) ||
		o.ClaudeAccountID == "" || o.ClaudeMachine == "" {
		return errors.New("Claude token setup needs its owned account manifest, explicit account and machine, and pinned gate digest")
	}
	if err := SetupPath(fs, o.ClaudeAccountsPath, false); err != nil {
		return err
	}
	entry, err := fs.Lstat(o.ClaudeAccountsPath)
	if err != nil {
		return err
	}
	if entry.Kind != KindFile || entry.Mode.Perm()&0077 != 0 {
		return errors.New("Claude owned account manifest must be a real owner-only file; rerun explicit setup in a private root")
	}
	raw, err := fs.ReadFile(o.ClaudeAccountsPath)
	if err != nil {
		return err
	}
	return claudeTokenManifestSelection(o, raw)
}

// planClaudeTokenAdmission reads only a reviewed public manifest. Apply
// materializes its exact bytes under the owned config root; PROFILE publication
// and subsequent install/run recheck this copy rather than a source-host path.
func planClaudeTokenAdmission(o SetupOptions, fs SettingsFS) ([]SetupStep, []SetupFact, error) {
	if o.ClaudeAccountsPath != ClaudeAccountsOwnedPath(o.ConfigDir) || !claudeAccountsDigest.MatchString(o.ClaudeAccountsSHA256) ||
		o.ClaudeAccountID == "" || o.ClaudeMachine == "" {
		return nil, nil, errors.New("Claude token setup needs --claude-account-id, --claude-machine, --claude-accounts and --claude-accounts-sha256 from the reviewed gate receipt")
	}
	facts := []SetupFact{
		{Name: "claude_account_id", State: "selected_unverified", Detail: o.ClaudeAccountID},
		{Name: "claude_machine", State: "selected_unverified", Detail: o.ClaudeMachine},
		{Name: "claude_accounts_sha256", State: "pinned_public", Detail: o.ClaudeAccountsSHA256},
	}
	if err := SetupPath(fs, o.ClaudeAccountsPath, false); err != nil {
		return nil, nil, err
	}
	entry, err := fs.Lstat(o.ClaudeAccountsPath)
	if err != nil {
		return nil, nil, err
	}
	if entry.Kind != KindMissing {
		if err := claudeOwnedAdmission(o, fs); err != nil {
			return nil, nil, err
		}
		if o.ClaudeAccountsSource != "" {
			raw, err := secrets.ReadClaudeTokenFleetManifestBytes(o.ClaudeAccountsSource)
			if err != nil || claudeTokenManifestSelection(o, raw) != nil {
				return nil, nil, errors.New("Claude source account manifest does not match the pinned owned copy; preserve this root and inspect the reviewed selection")
			}
			owned, err := fs.ReadFile(o.ClaudeAccountsPath)
			if err != nil || !bytes.Equal(owned, raw) {
				return nil, nil, errors.New("Claude source account manifest differs from the pinned owned copy; preserve this root and inspect the reviewed selection")
			}
		}
		return nil, facts, nil
	}
	if o.ClaudeAccountsSource == "" {
		return nil, nil, errors.New("Claude owned account manifest is missing; provide the reviewed public TSV for Go setup materialization")
	}
	raw, err := secrets.ReadClaudeTokenFleetManifestBytes(o.ClaudeAccountsSource)
	if err != nil {
		return nil, nil, err
	}
	if err := claudeTokenManifestSelection(o, raw); err != nil {
		return nil, nil, err
	}
	step := SetupStep{Name: "claude_account_manifest", Kind: "local_write", Path: o.ClaudeAccountsPath, Want: "owner-only copy of pinned public account bindings"}
	step.Apply = func() ([]SetupFact, error) {
		fresh, err := secrets.ReadClaudeTokenFleetManifestBytes(o.ClaudeAccountsSource)
		if err != nil {
			return nil, err
		}
		if err := claudeTokenManifestSelection(o, fresh); err != nil {
			return nil, err
		}
		if err := SetupPath(fs, o.ClaudeAccountsPath, false); err != nil {
			return nil, err
		}
		entry, err := fs.Lstat(o.ClaudeAccountsPath)
		if err != nil || entry.Kind != KindMissing {
			return nil, errors.New("Claude account manifest appeared during setup; preserve the owned root and rerun inspection")
		}
		if err := fs.WriteFile(o.ClaudeAccountsPath, fresh, 0o600); err != nil {
			return nil, err
		}
		if err := claudeOwnedAdmission(o, fs); err != nil {
			return nil, err
		}
		readback, err := fs.ReadFile(o.ClaudeAccountsPath)
		if err != nil || !bytes.Equal(readback, fresh) {
			return nil, errors.New("Claude account manifest readback differs; rerun setup inspection before activation")
		}
		return []SetupFact{{Name: "claude_account_manifest", State: "materialized_public", Detail: o.ClaudeAccountsPath}}, nil
	}
	return []SetupStep{step}, facts, nil
}
