RESULT tools22-rule-tokens-3-L2052 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 3 says?
CONFORMS cmd/nova-tokens/main.go:724
SPEC docs/SPEC-TOKENS.md:2052 rule 3
PKG internal/tokens
ASK When a transcript directory holds both readable and unreadable files, the tool must print TOKENS UNREADABLE naming file and OS reason, print TOKENS SOURCE with files=2 unreadable=1, write the day file from the readable file, print TOKENS FAIL unreadable=1 to stderr and exit 1; without the unreadable file the same run prints TOKENS OK and exits 0.

Deciding lines:

- `internal/tokens/claude.go:188-191` — `func (s *Source) unreadable(path, why string)` records each unreadable file, incrementing `s.Stat.Unreadable` and appending to `s.Unreadables`.
- `cmd/nova-tokens/main.go:472-476` — `unreadableLine` formats `"TOKENS UNREADABLE label=%s path=%s: %s"` with label, path, and OS reason.
- `cmd/nova-tokens/main.go:427-437` — `sourceLine` formats `"TOKENS SOURCE … files=%s unreadable=%s …"` per source.
- `cmd/nova-tokens/main.go:568` — each source's TOKENS SOURCE line is printed to stdout.
- `cmd/nova-tokens/main.go:572-574` — each source's Unreadables are printed as TOKENS UNREADABLE lines to stderr.
- `cmd/nova-tokens/main.go:722-731` — counts string includes `unreadable=%d`; if `bad` (unreadable.Total()>0 etc.) prints `TOKENS FAIL` to stderr, else `TOKENS OK` to stdout.
- `cmd/nova-tokens/main.go:735-737` — `return 1` on FAIL, `return 0` on OK.

GUARDED-BY cmd/nova-tokens/demanded_test.go:111 TestRule3AnUnreadableSourceIsCountedAndPrintedAndExitsOne
GUARDED-BY cmd/nova-tokens/demanded_test.go:148 TestRule3AValidLineIsNeverCountedAsNotJSON

Greps run:
```
grep -rn "TOKENS UNREADABLE" --include='*.go' internal/tokens/
grep -rn "unreadable" --include='*.go' internal/tokens/
grep -rn 'TOKENS FAIL|TOKENS OK|TOKENS SOURCE' --include='*.go' internal/tokens/
grep -rn '"TOKENS UNREADABLE|"TOKENS SOURCE|"TOKENS FAIL|"TOKENS OK' --include='*.go' .
grep -rn 'TOKENS UNREADABLE|TOKENS SOURCE' --include='*.go' cmd/nova-tokens/
grep -rn '"TOKENS SOURCE|"TOKENS UNREADABLE|unreadable\.Line|unreadableLine|source\.Line' --include='*.go' cmd/nova-tokens/
grep -rn "TOKENS" --include='*.go' internal/tokens/
```

Left owed.