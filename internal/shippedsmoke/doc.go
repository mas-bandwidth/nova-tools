// Package shippedsmoke holds the smoke test of a SHIPPED nova-check binary: the
// executable a release puts in a user's hands, not one built from the tree
// under test. `go test` covers the packages; nothing else covers the program a
// user actually runs.
//
// The test lives behind the build tag shippedsmoke, so it is not part of the
// unit tier, and reads the binary's path from NOVA_SHIPPED_BIN. The
// certification workflow's smoke job takes each hosted runner's shipped binary
// out of the release build, sets that variable and runs
//
//	go test -tags shippedsmoke -count=1 -v ./internal/shippedsmoke
//
// on ubuntu, macos and windows. The package has no dependencies beyond the
// standard library, so the job's Go compile stays inside its two minutes.
package shippedsmoke
