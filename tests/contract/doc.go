// Package contract pins docs/CLI-STYLE.md to the real binaries.
//
// It holds no code, only a test: contract_test.go builds each nova tool
// (bus, cairn, config, fleet, reserve, sandbox, secrets; a directory absent from the
// checkout is skipped by name) with go build into a temporary directory, runs
// it as a subprocess with a scrubbed environment and every path under the
// temporary directory, and asserts the help, flag, verb, missing-input and
// exit-code rules of CLI-STYLE.md (b), (d), (f) and (g).
//
// Banners differ per tool, so the verbs of each tool are listed in the test
// (toolSpecs) and each is checked to appear in the tool's own help banner.
// Adding a verb to a tool means adding it to toolSpecs; the test then holds it
// to the contract. Every failure message starts with an assertion id (A1..A6),
// then names the tool and verb, what was expected and what was seen.
//
// The package is test-only and needs no network and no real store.
package contract
