package secrets

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// place.go implements issue #764: `nova-secrets place` copies one named secret from the
// local store to a remote fleet machine over ssh with mode 0600, never prints or logs the
// value, and writes a receipt (machine, secret, path, the sealed file it came from, stamp).
// `nova-secrets placed --machine <name>` reads those receipts back.
//
// The machine's ssh target comes from the fleet registry file (nova-work CONFIG's machines
// section, materialised as the tab-separated fleet file nova-pulse already reads). The value
// travels to the machine on the ssh child's stdin, never in an argument list.
//
// What was placed is identified by the SEALED file, never by the value (docs/SPEC-SECRETS.md,
// "Nothing derived from a value"): the git blob id of the seat file's bytes, read once and
// decrypted from a private copy of those same bytes, and the store's HEAD commit read when
// place started. The blob id is a hash of the ciphertext, which sops encrypts under a
// random data key, so a reader without the key can test no guess of the value against it,
// unlike a hash of the value itself, which a short value gives up to anyone who tries
// candidates. Whether the machine holds the store's committed value is a comparison of two
// public ids: the receipt's blob against `git -C <store> rev-parse HEAD:<file>`.

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
	// DryRun prints the plan and writes nothing: the seat file is decrypted (the read the
	// verb needs to know the secret exists) but no ssh child runs and no receipt is written.
	DryRun bool
	Now    func() time.Time
}

// PlacedInput is one `nova-secrets placed` invocation.
type PlacedInput struct {
	Machine  string
	Receipts string
}

// placedReceipt is one line of a machine's receipt file: six tab-separated fields,
// secret, path, file, head, blob, stamp. It holds nothing derived from the value: File is
// the seat file (<seat>.yaml) the value was sealed in; Blob the git blob id of that file's
// bytes exactly as place read and decrypted them; Head the commit the store's HEAD named
// when place started ("-" when it named none), which holds those bytes unless the file had
// an uncommitted change.
//
// A line written by an older build has four fields, the third an unkeyed sha256 of the
// value. It is read with that field DROPPED, never kept, compared or printed: File, Head
// and Blob are "", Unknown and Legacy are set, so it lists as identity=unknown, and the
// next write of the machine's receipt file rewrites it as "-" fields, without the digest.
type placedReceipt struct {
	Secret  string
	Path    string
	File    string
	Head    string
	Blob    string
	Stamp   string
	Unknown bool // no sealed-file identity: place again
	Legacy  bool // read from an older build's line, whose file still holds a digest
}

// dash is a receipt value as its file holds it: "-" for one it does not hold.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// field is a receipt value as a line prints it.
func field(s string) string { return oneline.Field(dash(s)) }

// storeHead is the commit the store's HEAD names, read once from .git as files when place
// starts; "" when it names none yet. It records which commit the store stood on, and is no
// claim that this commit's tree holds the placed bytes: an uncommitted reseal can differ.
func storeHead(storeDir string) string {
	gitDir := filepath.Join(storeDir, ".git")
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(raw))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		// A branch with no commit yet resolves to nothing, and the receipt says head=-.
		if head, err = resolveRef(gitDir, ref); err != nil {
			return ""
		}
	}
	if !isValidHexSHA(head) {
		return ""
	}
	return head
}

// removeUnder removes the private snapshot directory. Tests replace it to make the
// removal fail deterministically; shipped commands always use safepath.RemoveUnder.
var removeUnder = safepath.RemoveUnder

// decryptSnapshot decrypts sealed, the seat file's bytes as place read them ONCE, from a
// private copy, never from the store's pathname a second time: the bytes sops decrypts are
// then exactly the bytes the receipt's blob id names, whatever happens to the store's file
// meanwhile (a reseal between two reads would deliver the old value under the new blob).
// The copy keeps the file's name, which sops reads the format from, at mode 0600 in a
// fresh 0700 directory under the process's temp dir, and its removal is tried on every
// path out. A copy left behind is never left silently: a failed removal is returned,
// joined by errors.Join with the decrypt's or the write's own error when that step had
// already failed, and the plaintext is zeroed and not returned, so no caller places a
// value read through a snapshot that is still on disk.
func decryptSnapshot(sopsPath, keyPath, file string, sealed []byte) (out []byte, err error) {
	dir, err := os.MkdirTemp("", "nova-secrets-place-*")
	if err != nil {
		return nil, fmt.Errorf("cannot make a private snapshot directory for %s: %w", file, err)
	}
	defer func() {
		rmErr := removeUnder(os.TempDir(), dir)
		if rmErr == nil {
			return
		}
		clear(out)
		out = nil
		err = errors.Join(err, fmt.Errorf("cannot remove the private snapshot %s: %w; remove it: rm -r %s", dir, rmErr, dir))
	}()
	snapshot := filepath.Join(dir, filepath.Base(file))
	if err := os.WriteFile(snapshot, sealed, 0o600); err != nil {
		return nil, fmt.Errorf("cannot write the private snapshot of %s: %w", file, err)
	}
	out, err = DecryptFile(sopsPath, keyPath, snapshot)
	if err != nil {
		// The snapshot is gone when this is read; the remedy names the store's file.
		return nil, errors.New(strings.ReplaceAll(err.Error(), snapshot, file))
	}
	return out, nil
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
	if err := preflight(in.StoreDir, need{in.Machine, "--machine <name>", false}, need{in.Secret, "--secret <name>", false},
		need{in.StoreDir, "--store <dir> (the local store to copy from)", false}, need{in.AsName, "--as <name>", true},
		need{in.KeyPath, "--key <path>", false}, need{in.SopsPath, "--sops <path>", false}); err != nil {
		return "", err
	}
	if !IsValidEnvVar(in.Secret) {
		return "", fmt.Errorf("invalid secret name %q: must match [A-Za-z_][A-Za-z0-9_]*", in.Secret)
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

	// The local store's shape was checked with the flags; its seat's file is checked here.
	targetFile := filepath.Join(in.StoreDir, in.AsName+".yaml")
	if _, err := os.Stat(targetFile); err != nil {
		return "", seatAbsent(in.StoreDir, in.AsName)
	}
	head := storeHead(in.StoreDir)

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
		return "", fmt.Errorf("fleet registry %s: %w; pass --machines <file>, one machine per line: name, ssh target, home, tab separated", in.Machines, err)
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

	// Read the sealed file once; its blob id and the decrypt both come from these bytes.
	sealed, err := os.ReadFile(targetFile)
	if err != nil {
		return "", fmt.Errorf("cannot read the sealed file %s: %w", targetFile, err)
	}
	blobID := GitBlobSHA1(sealed)
	decData, err := decryptSnapshot(in.SopsPath, in.KeyPath, targetFile, sealed)
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

	blob := hex.EncodeToString(blobID[:])
	receipt := placedReceipt{Secret: in.Secret, Path: remotePath, File: in.AsName + ".yaml", Head: head, Blob: blob}

	if in.DryRun {
		return placeDryRun(in, machine, receipt)
	}

	if err := sec.Use(func(value string) error {
		return sshPlaceSecret(in.SSH, machine.Target, remotePath, value)
	}); err != nil {
		return "", err
	}

	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	receipt.Stamp = now().UTC().Format(time.RFC3339)
	if err := writeReceipt(in.Receipts, in.Machine, receipt); err != nil {
		return "", err
	}

	return fmt.Sprintf("SECRETS PLACE OK machine=%s secret=%s path=%s file=%s head=%s blob=%s stamp=%s",
		oneline.Field(in.Machine), oneline.Field(in.Secret), oneline.Field(remotePath),
		field(receipt.File), field(head), field(blob), oneline.Field(receipt.Stamp)), nil
}

// placeDryRun is `place --dry-run`: every refusal RunPlace has already passed by the time
// it is called (the store, the key, the registry, the machine, the path, the secret in the
// seat file), then the plan the real run takes -- the machine and ssh target, the remote
// path and mode, the sealed file the receipt would record, and whether that receipt is
// added, replaced or already records this sealed file at this path -- and nothing written:
// no ssh child, no receipt, not even the receipts directory. The value is not read here.
func placeDryRun(in PlaceInput, machine FleetMachine, want placedReceipt) (string, error) {
	receipts, err := readReceipts(in.Receipts, in.Machine)
	if err != nil {
		return "", err
	}
	action := "add"
	for _, r := range receipts {
		if r.Secret != in.Secret {
			continue
		}
		action = "replace"
		// The same ciphertext holds the same value; an old receipt's identity is unknown.
		if !r.Unknown && r.Blob == want.Blob && r.File == want.File && r.Path == want.Path {
			action = "unchanged"
		}
	}
	lines := []string{
		fmt.Sprintf("SECRETS PLACE PLAN machine=%s secret=%s path=%s mode=0600 file=%s head=%s blob=%s",
			oneline.Field(in.Machine), oneline.Field(in.Secret), oneline.Field(want.Path), field(want.File), field(want.Head), field(want.Blob)),
		fmt.Sprintf("SECRETS PLACE PLAN ssh=%s target=%s writes=%s the value travels on stdin, never in an argument",
			oneline.Field(in.SSH), oneline.Field(machine.Target), oneline.Field(want.Path)),
		fmt.Sprintf("SECRETS PLACE PLAN receipt=%s action=%s",
			oneline.Field(receiptPath(in.Receipts, in.Machine)), action),
		fmt.Sprintf("SECRETS PLACE DRY-RUN OK machine=%s secret=%s nothing written, no ssh run",
			oneline.Field(in.Machine), oneline.Field(in.Secret)),
	}
	return strings.Join(lines, "\n"), nil
}

// RunPlaced lists the receipts written for one machine: each secret, its remote path, the
// sealed file it was placed from (file, head, blob) and its stamp. A receipt from an older
// build lists as identity=unknown, and a NOTE says its file still holds an old digest on
// disk and how to rewrite or remove it.
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
	items := make([]string, 0, len(receipts)+1)
	unknown, legacy := 0, 0
	for _, r := range receipts {
		line := fmt.Sprintf("SECRETS PLACED ITEM machine=%s secret=%s path=%s file=%s head=%s blob=%s stamp=%s",
			oneline.Field(in.Machine), oneline.Field(r.Secret), oneline.Field(r.Path),
			field(r.File), field(r.Head), field(r.Blob), oneline.Field(r.Stamp))
		if r.Unknown {
			line += " identity=unknown"
			unknown++
		}
		if r.Legacy {
			legacy++
		}
		items = append(items, line)
	}
	if unknown > 0 {
		file := oneline.Field(receiptPath(in.Receipts, in.Machine))
		note := fmt.Sprintf("SECRETS PLACED NOTE %d receipt(s) for %s carry no sealed-file identity (identity=unknown: place again)", unknown, oneline.Field(in.Machine))
		if legacy > 0 {
			note += fmt.Sprintf("; %d were written by an older build and still hold a hash of the value on disk, never read or shown, until the next place to %s rewrites %s",
				legacy, oneline.Field(in.Machine), file)
		}
		items = append(items, note+fmt.Sprintf("; run: nova-secrets place --machine %s --secret <NAME> ... for each, or remove the file: rm %s",
			oneline.Field(in.Machine), file))
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
	cmd, cancel := subproc.Command(context.Background(), subproc.SSH, sshPath, target, remoteCmd)
	defer cancel()
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
		switch len(fields) {
		case 6:
			r := placedReceipt{Secret: fields[0], Path: fields[1], File: fields[2], Head: fields[3], Blob: fields[4], Stamp: fields[5]}
			for _, f := range []*string{&r.File, &r.Head, &r.Blob} {
				if *f == "-" {
					*f = ""
				}
			}
			r.Unknown = r.Blob == ""
			out = append(out, r)
		case 4:
			// An older build's line: secret, path, sha256 of the value, stamp. The digest
			// (fields[2]) is dropped here and goes no further.
			out = append(out, placedReceipt{Secret: fields[0], Path: fields[1], Stamp: fields[3], Unknown: true, Legacy: true})
		default:
			return nil, fmt.Errorf("receipt %s line %d: want 6 tab-separated fields (secret, path, file, head, blob, stamp); run: rm %s and place again", receiptPath(dir, machine), n+1, receiptPath(dir, machine))
		}
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
	// Every line is written in the six-field form, so an older build's line loses its
	// digest here: its file, head and blob are written as "-".
	for _, r := range existing {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Secret, r.Path, dash(r.File), dash(r.Head), dash(r.Blob), r.Stamp)
	}

	final := receiptPath(dir, machine)
	return atomicfile.Write(filepath.Clean(final), []byte(b.String()), 0o600, atomicfile.ExactMode())
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
