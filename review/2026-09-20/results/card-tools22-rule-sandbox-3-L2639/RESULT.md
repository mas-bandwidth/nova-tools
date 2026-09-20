RESULT tools22-rule-sandbox-3-L2639 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 3 says?
CONFORMS profiles/darwin.sb.tmpl:95-101
SPEC docs/SPEC-SANDBOX.md:2639 rule 3
PKG internal/sandbox
ASK The darwin sandbox profile must restrict mach-lookup to exactly three named services (opendirectoryd.libinfo, SecurityServer, system.logger), deny unqualified (allow mach-lookup) so pbpaste cannot read the clipboard, and name the remaining width (launchctl print, security list-keychains file names) explicitly.

The template profiles/darwin.sb.tmpl:95-101 hardcodes the narrowed mach-lookup block with exactly the three named services and no unqualified form:
```
(allow mach-lookup
  (global-name "com.apple.system.opendirectoryd.libinfo")
  (global-name "com.apple.SecurityServer")
  (global-name "com.apple.system.logger"))
```

The template comment (lines 80-94) documents the measurement process, names the accepted width (`launchctl print system`, `security list-keychains` file names), and states that pbpaste is denied. The profile is never widened by the Go generator (profile.go:143-148 confirms metal adds no mach-lookup service).

GUARDED-BY internal/sandbox/policy_test.go:647 TestMachLookupIsNarrowed — asserts no unqualified `(allow mach-lookup)` and that all three named services are present (confirmed pass). Also guarded by profiles/darwin-check.sh:245 `expect_deny clipboard_denied "pbpaste > /dev/null"`.

Grep results: `grep -rn "mach.lookup\|mach-lookup\|mach_lookup" --include='*.go' internal/sandbox/` found 8 matches in gpu.go:14, policy_test.go:646/659/660/669, profile.go:143/148, policy.go:468. All confirm narrowed mach-lookup with no widening.

Left owed: None.
git status --short: (no output)