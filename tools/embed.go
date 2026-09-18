// Package tools exists for ONE reason: `go:embed` cannot reach above its own package, and
// the provisioning standard must be readable from a binary that is running nowhere near a
// clone.
//
// `nova-pulse fleet certify` hashes the provisioning standard, and a certificate is current
// only under that hash. The launchd job that runs the fleet every six hours runs from
// wherever launchd starts it, and on 2026-09-18 the first real fleet-wide run refused with
// `tools/bench-standard.sh not found above the working directory`. Copying the file into an
// internal package would have made two standards; a package HERE, beside the file, keeps one.
package tools

import _ "embed"

// BenchStandard is tools/bench-standard.sh, verbatim. It is the default `--standard`, and
// because it is the same bytes as the file, a machine certified from a clone and a machine
// certified by the timer are held to the same standard and hash alike.
//
//go:embed bench-standard.sh
var BenchStandard []byte
