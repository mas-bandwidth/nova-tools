package release

// ADOPT INVALIDATES CERTIFICATES, so adopt can renew them.
//
// A certificate says: this machine did this kind of work, under this build, held against
// this standard. `release adopt` changes the build on every machine it touches, so the
// moment it succeeds every certificate those machines held stops being current and the fill
// will refuse their cards until somebody runs certification again. That is correct -- a new
// build is a new machine as far as "can it do the work" goes -- but leaving it to somebody
// is how a fleet spends an afternoon uncertified.
//
// `--certify` closes it: after the install lands on a machine, the same engine
// `nova-pulse fleet certify` runs is run here, for that machine, under the version that was
// just installed, and its rows are appended. Behind the SAME seam: the certification talks
// to the machine through this package's own SSH edge, so a test that fakes ssh fakes the
// certification too and nothing here opens a socket.

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// sshRemote adapts this package's argv-shaped SSH edge to the script-shaped remote the
// certification engine wants.
//
// The script is carried BASE64 ENCODED and decoded on the far side. That is not decoration:
// SSH.Run hands its argv entries to ssh separately and the remote's own shell reassembles
// them with spaces, so a workload body -- which is heredocs, quoting and newlines -- cannot
// survive as argv. Base64 is one word of [A-Za-z0-9+/=], which survives any reassembly, and
// `openssl base64` decodes it on both a Linux bench and a Mac one.
type sshRemote struct {
	ssh SSH
	// parent is the whole run's deadline. The engine bounds each workload on its own, and
	// the two are ANDed here, so `--timeout` on the release still ends the release.
	parent context.Context
}

func (r sshRemote) Run(ctx context.Context, target, script string) (string, error) {
	bounded, cancel := r.parent, context.CancelFunc(func() {})
	if deadline, ok := ctx.Deadline(); ok {
		bounded, cancel = context.WithDeadline(r.parent, deadline)
	}
	defer cancel()
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	return r.ssh.Run(bounded, target, []string{
		"printf", "%s", encoded, "|", "openssl", "base64", "-d", "-A", "|", "sh",
	})
}

// certifyAdopted runs certification on one machine that has just adopted a version. It
// answers how many workloads failed, so the adopt line can carry it, and writes every line
// it produces to the same two streams the adopt uses.
func certifyAdopted(ctx context.Context, o options, ssh SSH, machine, version string, out, errs io.Writer) int {
	loads, err := fleet.StandardWorkloads()
	if err != nil {
		fmt.Fprintf(errs, "CERTIFY REFUSED machine=%s: %s\n", field(machine), oneLine("", err))
		return 1
	}
	hash, err := fleet.StandardHash(o.standard, loads)
	if err != nil {
		fmt.Fprintf(errs, "CERTIFY REFUSED machine=%s: %s\n", field(machine), oneLine("", err))
		return 1
	}
	progress(errs, "certifying %s on %s", version, machine)
	code := fleet.Certify(fleet.CertifyInput{
		Machines: o.certify, Only: machine, Workloads: loads,
		Certs: o.certs, Hash: hash,
		// The build is not asked of the machine here: this verb just PUT it there, and the
		// receipt it read back is what says so.
		Build:   version,
		Timeout: o.timeout,
		Remote:  sshRemote{ssh: ssh, parent: ctx},
		Stdout:  out, Stderr: errs,
	})
	if code == 0 {
		return 0
	}
	return 1
}
