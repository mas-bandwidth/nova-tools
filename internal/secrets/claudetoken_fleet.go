package secrets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ClaudeTokenFleetManifest binds explicit logical accounts and profiles to
// machine-specific seats. Labels are for humans; IDs, not labels, are keys.
// The TSV holds no token value and does not infer identity from an auth file.
type ClaudeTokenFleetManifest struct {
	Accounts []ClaudeTokenAccountBinding
}

type ClaudeTokenAccountBinding struct {
	ID, Label, SourceMachine, SourceSeat, IssuedAt string
	Targets                                        []ClaudeTokenTargetBinding
}

type ClaudeTokenTargetBinding struct {
	Machine, Profile, Seat string
}

type ClaudeTokenAccountPlan struct {
	ID, Label, SourceMachine string
	Token                    ClaudeTokenPlan
	Targets                  []ClaudeTokenTargetPlan
}

type ClaudeTokenTargetPlan struct {
	Machine, Profile, Seat, State string
}

type ClaudeTokenFleetPlan struct {
	Accounts []ClaudeTokenAccountPlan
	Targets  int
}

const claudeFleetHeader = "account_id\tlabel\tsource_machine\tsource_seat\tmachine\tprofile_id\ttarget_seat\tissued_at"

// ReadClaudeTokenFleetManifest reads one bounded public manifest. Symlinked
// path components are refused so the named file stays tied to the reviewed path.
func ReadClaudeTokenFleetManifest(path string) (ClaudeTokenFleetManifest, error) {
	raw, err := ReadClaudeTokenFleetManifestBytes(path)
	if err != nil {
		return ClaudeTokenFleetManifest{}, err
	}
	return ParseClaudeTokenFleetManifest(raw)
}

// ReadClaudeTokenFleetManifestBytes returns bounded public bytes for an owned
// copy. It never reads a seat file or a credential value.
func ReadClaudeTokenFleetManifestBytes(path string) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("Claude token manifest must be a real regular file; name a reviewed TSV file")
	}
	raw, err := readClaudeFleetRegularNoFollow(abs, 64<<10)
	if err != nil || !utf8.Valid(raw) {
		return nil, fmt.Errorf("Claude token manifest must be a real regular file with readable UTF-8 and at most 64 KiB")
	}
	return raw, nil
}

// ParseClaudeTokenFleetManifest validates the exact public bytes that setup
// copies and pins. No credential values are accepted by its schema.
func ParseClaudeTokenFleetManifest(raw []byte) (ClaudeTokenFleetManifest, error) {
	if len(raw) > 64<<10 || !utf8.Valid(raw) {
		return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest must be a readable UTF-8 regular file of at most 64 KiB")
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) < 2 || strings.TrimSuffix(lines[0], "\r") != claudeFleetHeader || len(lines)-1 > 64 {
		return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest needs its exact eight-column header and 1–64 target rows")
	}
	manifest := ClaudeTokenFleetManifest{}
	byID := make(map[string]int)
	profileOwner := make(map[string]string)
	profileMachine := make(map[string]bool)
	seatOwner := make(map[string]string)
	sourceOwner := make(map[string]string)
	for i, rawLine := range lines[1:] {
		line := strings.TrimSuffix(rawLine, "\r")
		fields := strings.Split(line, "\t")
		if len(fields) != 8 {
			return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest line %d needs eight tab-separated fields", i+2)
		}
		id, label, sourceMachine, source, machine, profile, seat, issued := fields[0], fields[1], fields[2], fields[3], fields[4], fields[5], fields[6], fields[7]
		if !claudeFleetName(id) || !claudeFleetName(sourceMachine) || !claudeFleetName(source) || !claudeFleetName(machine) || !claudeFleetName(profile) || !claudeFleetName(seat) ||
			strings.TrimSpace(label) == "" || strings.TrimSpace(label) != label || utf8.RuneCountInString(label) > 80 ||
			strings.IndexFunc(label, unicode.IsControl) >= 0 || strings.TrimSpace(issued) != issued {
			return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest line %d needs explicit safe account, label, source machine, source seat, target machine, profile and target seat fields", i+2)
		}
		if owner, used := profileOwner[profile]; used && owner != id {
			return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest profile %s is shared across accounts; keep one stable account per profile", profile)
		}
		pair := machine + "\x00" + profile
		if profileMachine[pair] {
			return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest profile %s is bound twice on machine %s; use one seat per machine/profile", profile, machine)
		}
		if owner, used := seatOwner[seat]; used {
			return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest target seat %s is shared with account %s; give every profile its own seat", seat, owner)
		}
		if owner, used := sourceOwner[source]; used && owner != id {
			return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest source seat %s is shared across accounts; give each account its own source seat", source)
		}
		profileOwner[profile], profileMachine[pair], seatOwner[seat], sourceOwner[source] = id, true, id, id
		idx, exists := byID[id]
		if !exists {
			if len(manifest.Accounts) == 32 {
				return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest exceeds 32 accounts")
			}
			idx = len(manifest.Accounts)
			byID[id] = idx
			manifest.Accounts = append(manifest.Accounts, ClaudeTokenAccountBinding{ID: id, Label: label, SourceMachine: sourceMachine, SourceSeat: source, IssuedAt: issued})
		} else {
			account := manifest.Accounts[idx]
			if account.Label != label || account.SourceMachine != sourceMachine || account.SourceSeat != source || account.IssuedAt != issued {
				return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest account %s changes its label, source machine, source seat or issue time between rows", id)
			}
		}
		manifest.Accounts[idx].Targets = append(manifest.Accounts[idx].Targets, ClaudeTokenTargetBinding{Machine: machine, Profile: profile, Seat: seat})
	}
	for _, account := range manifest.Accounts {
		if owner, used := seatOwner[account.SourceSeat]; used {
			return ClaudeTokenFleetManifest{}, fmt.Errorf("Claude token manifest source seat %s is also a target seat for account %s; keep source and target seats separate", account.SourceSeat, owner)
		}
	}
	return manifest, nil
}

// ClaudeTokenFleetSHA256 is a public-byte pin, not credential or entitlement
// proof. A changed manifest requires a new explicit setup selection.
func ClaudeTokenFleetSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func claudeFleetName(name string) bool { return len(name) <= 64 && IsValidAsName(name) }

// PlanClaudeTokenFleet checks every explicitly bound account separately. It
// does not verify machine registry admission, credential validity or provider
// allowance; those gates precede any use of a generated setup profile.
func PlanClaudeTokenFleet(store string, manifest ClaudeTokenFleetManifest, now time.Time) (ClaudeTokenFleetPlan, error) {
	if len(manifest.Accounts) == 0 || len(manifest.Accounts) > 32 {
		return ClaudeTokenFleetPlan{}, fmt.Errorf("Claude token fleet needs 1–32 explicit account bindings")
	}
	out := ClaudeTokenFleetPlan{}
	for _, account := range manifest.Accounts {
		seats := make([]string, 0, len(account.Targets))
		for _, target := range account.Targets {
			seats = append(seats, target.Seat)
		}
		plan, err := PlanClaudeToken(store, account.SourceSeat, seats, account.IssuedAt, now)
		if err != nil {
			return ClaudeTokenFleetPlan{}, fmt.Errorf("Claude token account %s: %w", account.ID, err)
		}
		row := ClaudeTokenAccountPlan{ID: account.ID, Label: account.Label, SourceMachine: account.SourceMachine, Token: plan}
		for i, target := range account.Targets {
			row.Targets = append(row.Targets, ClaudeTokenTargetPlan{Machine: target.Machine, Profile: target.Profile, Seat: target.Seat, State: plan.Targets[i].State})
		}
		out.Accounts = append(out.Accounts, row)
		out.Targets += len(row.Targets)
	}
	return out, nil
}

func PlanClaudeTokenFleetNow(store, manifestPath string) (ClaudeTokenFleetPlan, error) {
	manifest, err := ReadClaudeTokenFleetManifest(manifestPath)
	if err != nil {
		return ClaudeTokenFleetPlan{}, err
	}
	return PlanClaudeTokenFleet(store, manifest, time.Now())
}
