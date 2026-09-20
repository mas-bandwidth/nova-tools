RESULT tools22-rule-version-8-L130 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 8 says?
CONFORMS cmd/nova-version/main.go:19
SPEC docs/SPEC-VERSION.md:130 rule 8
PKG cmd/nova-version
ASK Every binary must answer both `version` and `--version` with the identical one-line output `<tool> <stamp> <goos>/<goarch> <go version>` at exit 0, and refuse any second argument or different flag spelling at exit 2.

Deciding lines:

cmd/nova-version/main.go:19:
    func main() { os.Exit(update.Main("nova-version", os.Args[1:], version, os.Stdout, os.Stderr)) }

internal/update/cli.go:158-164:
    if verb == "version" || verb == "--version" {
        if len(args) != 0 {
            return refusal(errs, tool, fmt.Errorf("version takes no arguments (run %s version)", name))
        }
        fmt.Fprintln(out, buildinfo.Line(name, stamp))
        return 0
    }

Both spellings ("version" and "--version") match on the same condition (cli.go:158), produce output through the single `buildinfo.Line(name, stamp)` call (cli.go:162) which yields the four-token line `<tool> <stamp> <goos>/<goarch> <go version>`, and return exit 0. Extra arguments cause `refusal(...)` which returns exit 2 (cli.go:160).

GUARDED-BY cmd/nova-version/version_test.go:10 TestVersionStampAndUsage

Grep used to find tests:
    grep -rn "func Test" --include='*_test.go' cmd/nova-version/
    Result: TestVersionStampAndUsage at version_test.go:10 iterates ["version", "--version"], asserts identical output shape, and checks exit 2 on extra args.

Left owed

git status --short
