package secrets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// place.go implements issue #764: `nova-secrets place` copies one named secret from the
// local store to a remote fleet machine over ssh with mode 0600, never prints or logs the
// value, and writes a receipt (machine, secret, path, sha256 of the value, stamp).
// `nova-secrets placed --machine <name>` reads those receipts back by name and hash.
//
// The machine's ssh target comes from the fleet registry file (nova-work CONFIG's machines
// section, materialised as the tab-separated fleet file nova-pulse already reads). The value
// travels to the machine on the ssh child's stdin, never in an argument list, and the only
// thing written down is its sha256.

// FleetMachine is one machine in the fleet registry: a name, the ssh target that reaches it,
// and its home directory (used as the default root for a placed secret).
type FleetMachine struct {
	Name   string
	Target string
	Home   string
}

// PlaceInput is one `nova-secrets place` invocation.
type PlaceInput struct {
	StoreDir   string
	AsName     string
	KeyPath    string
	SopsPath   string
	Machine    string
	Secret     string
	RemotePath string
	Machines   string
	Receipts   string
	SSH        string
	Now        func() time.Time
}

// PlacedInput is one `nova-secrets placed` invocation.
type PlacedInput struct {
	Machine  string
	Receipts string
}

// placedReceipt is one line of a machine's receipt file. It never holds the value.
type placedReceipt struct {
	Secret string
	Path   string
	SHA256 string
	Stamp  string
}

// ReadFleetMachines parses the tab-separated fleet registry: name, ssh target, home, and
// the optional fourth column the pulse fleet file carries. Blank lines and `#` comments are
// skipped; a line with fewer than two fields, or an empty name or target, is refused.
func ReadFleetMachines(path string) (map[string]FleetMachine, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	machines := make(map[string]FleetMachine)
	for n, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			return nil, fmt.Errorf("line %d wants at least 2 tab-separated fields name, ssh target, got %d", n+1, len(fields))
		}
		m := FleetMachine{Name: strings.TrimSpace(fields[0]), Target: strings.TrimSpace(fields[1])}
		if len(fields) >= 3 {
			m.Home = strings.TrimSpace(fields[2])
		}
		if m.Name == "" || m.Target == "" {
			return nil, fmt.Errorf("line %d wants a name and an ssh target", n+1)
		}
		if _, dup := machines[m.Name]; dup {
			return nil, fmt.Errorf("line %d: machine %q is listed twice", n+1, m.Name)
		}
		machines[m.Name] = m
	}
	if len(machines) == 0 {
		return nil, fmt.Errorf("no machines; refusing to guess")
	}
	return machines, nil
}

// RunPlace copies one secret to one machine and records a receipt. It returns the one OK
// line, or an error whose text is safe to print (it never contains the value).
func RunPlace(in PlaceInput) (string, error) {
	if in.Machine == "" {
		return "", fmt.Errorf("missing --machine <name>")
	}
	if in.Secret == "" {
		return "", fmt.Errorf("missing --secret <name>")
	}
	if !IsValidEnvVar(in.Secret) {
		return "", fmt.Errorf("invalid secret name %q: must match [A-Za-z_][A-Za-z0-9_]*", in.Secret)
	}
	if in.StoreDir == "" {
		return "", fmt.Errorf("missing --store <dir>; the local secrets store to copy from")
	}
	if in.AsName == "" {
		return "", fmt.Errorf("missing --as <name>")
	}
	if !IsValidAsName(in.AsName) {
		return "", fmt.Errorf("invalid seat name %q: must match [A-Za-z0-9_-]+", in.AsName)
	}
	if in.KeyPath == "" {
		return "", fmt.Errorf("missing --key <path>")
	}
	if in.SopsPath == "" {
		return "", fmt.Errorf("missing --sops <path>")
	}
	if in.Machines == "" {
		in.Machines = defaultFleetFile()
	}
	if in.Receipts == "" {
		in.Receipts = defaultReceiptsDir()
	}
	if in.Receipts == "" {
		return "", fmt.Errorf("missing --receipts <dir> (and HOME is unset, so there is no default)")
	}
	if in.SSH == "" {
		in.SSH = "ssh"
	}

	// The local store, lightly: a directory that is a git working copy with a rule file.
	sFi, err := os.Stat(in.StoreDir)
	if err != nil || !sFi.IsDir() {
		return "", fmt.Errorf("store %s is not a directory; run: nova-secrets place --store <dir>", in.StoreDir)
	}
	if gFi, err := os.Stat(filepath.Join(in.StoreDir, ".git")); err != nil || !gFi.IsDir() {
		return "", fmt.Errorf("store %s has no .git directory", in.StoreDir)
	}
	if _, err := os.Stat(filepath.Join(in.StoreDir, ".sops.yaml")); err != nil {
		return "", fmt.Errorf("store %s carries no .sops.yaml", in.StoreDir)
	}
	targetFile := filepath.Join(in.StoreDir, in.AsName+".yaml")
	if _, err := os.Stat(targetFile); err != nil {
		return "", fmt.Errorf("store file %s is absent", targetFile)
	}

	if err := CheckInvariant6(in.KeyPath); err != nil {
		return "", err
	}
	if _, err := CheckSopsVersion(in.SopsPath); err != nil {
		return "", err
	}

	// The machine must be in the fleet registry; a route for a machine nobody registered is
	// exactly the case the remedy names.
	machines, err := ReadFleetMachines(in.Machines)
	if err != nil {
		return "", fmt.Errorf("fleet registry %s: %w; run: nova-pulse fleet survey --benches %s", in.Machines, err, in.Machines)
	}
	machine, ok := machines[in.Machine]
	if !ok {
		return "", fmt.Errorf("machine %s is not in the fleet registry %s; add it there", in.Machine, in.Machines)
	}

	remotePath := in.RemotePath
	if remotePath == "" {
		if machine.Home == "" {
			return "", fmt.Errorf("machine %s has no home in the fleet registry and no --path was given", in.Machine)
		}
		remotePath = filepath.ToSlash(filepath.Join(machine.Home, ".config", "nova-secrets", in.Secret+".env"))
	}

	// Decrypt the seat file and take the one named secret out of it.
	decData, err := DecryptFile(in.SopsPath, in.KeyPath, targetFile)
	if err != nil {
		return "", err
	}
	secretsMap, _, err := ParseDecryptedSecrets(decData)
	if err != nil {
		return "", err
	}
	sec, ok := secretsMap[in.Secret]
	if !ok {
		return "", fmt.Errorf("secret %s is not in %s; run: sops %s", in.Secret, targetFile, targetFile)
	}

	var hash string
	err = sec.Use(func(value string) error {
		sum := sha256.Sum256([]byte(value))
		hash = hex.EncodeToString(sum[:])
		return sshPlaceSecret(in.SSH, machine.Target, remotePath, value)
	})
	if err != nil {
		return "", err
	}

	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	stamp := now().UTC().Format(time.RFC3339)
	receipt := placedReceipt{Secret: in.Secret, Path: remotePath, SHA256: hash, Stamp: stamp}
	if err := writeReceipt(in.Receipts, in.Machine, receipt); err != nil {
		return "", err
	}

	return fmt.Sprintf("SECRETS PLACE OK machine=%s secret=%s path=%s sha256=%s stamp=%s",
		oneline.Field(in.Machine), oneline.Field(in.Secret), oneline.Field(remotePath),
		oneline.Field(hash), oneline.Field(stamp)), nil
}

// RunPlaced lists the receipts written for one machine, by name and hash.
func RunPlaced(in PlacedInput) (string, []string, error) {
	if in.Machine == "" {
		return "", nil, fmt.Errorf("missing --machine <name>")
	}
	if in.Receipts == "" {
		in.Receipts = defaultReceiptsDir()
	}
	if in.Receipts == "" {
		return "", nil, fmt.Errorf("missing --receipts <dir> (and HOME is unset, so there is no default)")
	}
	receipts, err := readReceipts(in.Receipts, in.Machine)
	if err != nil {
		return "", nil, err
	}
	items := make([]string, 0, len(receipts))
	for _, r := range receipts {
		items = append(items, fmt.Sprintf("SECRETS PLACED ITEM machine=%s secret=%s path=%s sha256=%s stamp=%s",
			oneline.Field(in.Machine), oneline.Field(r.Secret), oneline.Field(r.Path),
			oneline.Field(r.SHA256), oneline.Field(r.Stamp)))
	}
	okLine := fmt.Sprintf("SECRETS PLACED OK machine=%s count=%d", oneline.Field(in.Machine), len(receipts))
	return okLine, items, nil
}

// sshPlaceSecret writes value to remotePath over ssh with mode 0600. The value travels on
// stdin; the remote path is the only caller text in the command, shell-quoted.
func sshPlaceSecret(sshPath, target, remotePath, value string) error {
	remoteCmd := fmt.Sprintf("umask 077 && set -e && mkdir -p \"$(dirname %s)\" && cat > %s && chmod 600 %s",
		shSingleQuote(remotePath), shSingleQuote(remotePath), shSingleQuote(remotePath))
	testguard.RefuseHosts(sshPath, target, remoteCmd)
	cmd := exec.Command(sshPath, target, remoteCmd)
	cmd.Stdin = bytes.NewReader([]byte(value))
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Run(); err != nil {
		code := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
		// The remote transcript is withheld; it can carry a command's own output and this
		// path exists for a value, so nothing from it is printed.
		return fmt.Errorf("ssh to %s failed: exit %d (transcript withheld)", target, code)
	}
	return nil
}

// shSingleQuote wraps s in POSIX single quotes, escaping an embedded single quote, so the
// remote shell sees exactly the path this tool wrote.
func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// receiptPath names one machine's receipt file.
func receiptPath(dir, machine string) string {
	return filepath.Join(dir, machine+".receipt")
}

// readReceipts reads one machine's receipt file. An absent file is zero receipts, not a
// failure: a machine with nothing placed answers count=0.
func readReceipts(dir, machine string) ([]placedReceipt, error) {
	raw, err := os.ReadFile(receiptPath(dir, machine))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []placedReceipt
	for n, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("receipt %s line %d: want 4 tab-separated fields", receiptPath(dir, machine), n+1)
		}
		out = append(out, placedReceipt{Secret: fields[0], Path: fields[1], SHA256: fields[2], Stamp: fields[3]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Secret < out[j].Secret })
	return out, nil
}

// writeReceipt replaces this machine's entry for one secret and rewrites the file at 0600.
func writeReceipt(dir, machine string, add placedReceipt) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create receipts directory %s: %w", dir, err)
	}
	existing, err := readReceipts(dir, machine)
	if err != nil {
		return err
	}
	replaced := false
	for i := range existing {
		if existing[i].Secret == add.Secret {
			existing[i] = add
			replaced = true
		}
	}
	if !replaced {
		existing = append(existing, add)
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].Secret < existing[j].Secret })

	var b strings.Builder
	for _, r := range existing {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.Secret, r.Path, r.SHA256, r.Stamp)
	}

	final := receiptPath(dir, machine)
	tmp, err := os.CreateTemp(dir, machine+".receipt.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// defaultFleetFile is the fleet registry this tool reads when --machines is not given.
func defaultFleetFile() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "nova-tools", "fleet.tsv")
}

// defaultReceiptsDir is where receipts live when --receipts is not given.
func defaultReceiptsDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "nova-secrets", "placed")
}
