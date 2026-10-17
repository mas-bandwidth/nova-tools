# SPEC-SCHEMA-FUZZ: the neighbour oracle for the schema wire fuzz tests

This is the one source of the input-to-output oracle that every generated schema wire
package's native fuzz test carries. The 16 packages are nova-tools `pkg/buswire`,
`pkg/configwire`, `pkg/decidewire`, `pkg/friendfilewire`, `pkg/hookwire`,
`pkg/ledgerwire`, `pkg/pongwire`, `pkg/releasewire`, `pkg/seatrecwire`,
`pkg/swarmwire`, `pkg/tablemodelwire`, and in the nova-sprint repository
`internal/sprint/store/{donewire,friendrowswire,oprowswire,snaprowswire,viewrowswire}`
(donewire, friendrowswire, oprowswire, snaprowswire, viewrowswire). Each holds
`Fuzz<Name>` in its `fuzz_test.go` (the fuzz-<name> cards, stream v15-schema-tables); the
fuzz-<name>-prefix-oracle cards add the block below to each, and the coordinator's fuzz
card template and generator carry the same text. `internal/docs`
`TestSchemaFuzzSpecCarriesTheNeighbourOracle` holds this file to its shape. The CI tiers
the fuzz tests run in are in [SPEC-CI.md](SPEC-CI.md).

## The gap

`Fuzz<Name>` already asserts, for every root and every form (wire, base64, JSON): no
panic; every refusal is a named `Table*` error; every accepted value re-encodes to a wire
and a JSON image that read back to themselves in every form. Its only input-to-output
check, an accepted clean wire reproduces byte for byte, runs on seed inputs only
(`fuzzSeeds[string(in)]`). A reader that silently accepts damaged input therefore passes:
the damaged input is not a seed, and the value it yields is self-consistent. The reader on
amd-2 planted exactly that (plant2: `MachineBeatLoad`'s trailing-bytes branch returns true
with the report restored) and it survived the seeds and a 30 s fuzz run.

## The rule

For every accepted value, seed or not, take its re-encoded wire `W` (the canonical image
this build writes) and require both halves:

1. **Every strict prefix of `W` is refused**, with a named `Table*` error: `W[:n]` for every
   `n` in `0..len(W)-1`. A reader that treats a truncated wire as complete fails it.
2. **`W` with one byte appended (0x00, then 0xff) does not read clean**: it is refused, or it
   is accepted with a non-zero `Unknown`, `Widened`, `Retained`, `Clamped`, `KindMismatch` or
   `Duplicate` count (clean is defined exactly as the existing reproduce check defines it). A
   reader that ignores trailing bytes fails it.

Why each half is there:

- The one-byte-appended half is the half that catches the measured silent accept. Plant2
  in configwire `MachineBeatLoad` and the same plant in viewrowswire `StageStatLoad` are
  missed by the existing test and by the prefix half alone, and are caught by the
  appended-byte half on the seeds alone (the table below).
- The strict prefix half catches no planted bug measured here: the last 8 bytes of every
  root's wire are its id count, so cutting the tail breaks the framing and the reader
  refuses before any body rule runs. It stays because it is the other side of the same
  boundary (the planted prefix reader in the proof test is the class it closes: a reader that
  loses its length check), it costs one load per byte of an accepted wire, and it is green
  on every package on unplanted code, so it adds no false failure.
- Both halves test the wire the build wrote, never the fuzz input, so neither depends on the
  input being canonical.

Why the seed gate on the reproduce check stays: dropping it (compare every clean accepted
wire with its input) false-fails decidewire, friendfilewire, hookwire, pongwire and
seatrecwire on legitimate non-canonical images. A wire whose id table carries an id no field
uses is accepted clean and re-encodes without it, which is correct reading, not damage.
Measured here on decidewire at 2bc0d585e with the gate dropped, a 90 s run fails on root
`Usage`, selector 0x27, input
`01 00 30 30 30 30 30 30 30 30 30 30 30 30 30 30 30 31 30 30 30 30 30 30 30 32 30 30 30 30 30 30 30 37 04 00 00 00 00 00 00 00`,
which re-encodes to `01 00 00 00 00 00 00 00 00 00`. The neighbour rule closes the gap
without comparing input to output, so it holds on such images.

## The block

Per package, `@PKG@` is the package name (for example `buswire`) and `@FUZZ@` its fuzz test
name (for example `FuzzBuswire`). Inside the fuzz body, right after the closing brace of the
seed reproduce check (`if form == fuzzWire && fuzzSeeds[string(in)] && clean {`), add one
line at that indentation:

```text
require.Empty(t, fuzzNeighbours(root, wire), "%s as %d %q: an accepted value fails the neighbour oracle", root.name, form, in)
```

and append to the end of `fuzz_test.go` a blank line, this doc comment:

```text
// fuzzNeighbours is the input-to-output oracle for every accepted value, seed
// or not: each strict prefix of its wire is refused with a named Table* error,
// and its wire with one byte appended (0x00, then 0xff) does not read clean.
// It returns "" or what failed.
```

and then this text, verbatim (it is the text of the follow-on briefs, from the `func` line on):

```go
func fuzzNeighbours(root fuzzRoot, wire []byte) string {
	for n := 0; n < len(wire); n++ {
		_, _, _, err := root.load(fuzzWire, wire[:n])
		if err == nil {
			return fmt.Sprintf("%s: a strict prefix (%d of %d bytes) of an accepted wire is accepted", root.name, n, len(wire))
		}
		if !strings.HasPrefix(fmt.Sprintf("%T", err), "@PKG@.Table") {
			return fmt.Sprintf("%s: a prefix refusal with no name: %T %v", root.name, err, err)
		}
	}
	for _, x := range []byte{0x00, 0xff} {
		_, _, rep, err := root.load(fuzzWire, append(append([]byte{}, wire...), x))
		if err == nil && rep.Unknown == 0 && rep.Widened == 0 && rep.Retained == 0 && rep.Clamped == 0 && rep.KindMismatch == 0 && rep.Duplicate == 0 {
			return fmt.Sprintf("%s: an accepted wire plus one byte %02x reads clean", root.name, x)
		}
	}
	return ""
}

// Test@FUZZ@Neighbours proves fuzzNeighbours on every root: the real reader
// passes at its reset value, and two planted readers fail, one that accepts
// the reset wire with a byte appended and one that accepts a strict prefix.
func Test@FUZZ@Neighbours(t *testing.T) {
	t.Parallel()
	for _, root := range fuzzRoots {
		var rep TableReport
		wire, _, ok := root.run(fuzzEmpty, nil, &rep)
		require.True(t, ok, "%s: the reset value does not encode", root.name)
		require.Empty(t, fuzzNeighbours(root, wire))
		trailing := fuzzRoot{root.name, func(f fuzzForm, in []byte, rep *TableReport) ([]byte, []byte, bool) {
			if f == fuzzWire && len(in) > len(wire) && string(in[:len(wire)]) == string(wire) {
				in = wire
			}
			return root.run(f, in, rep)
		}}
		require.Contains(t, fuzzNeighbours(trailing, wire), "plus one byte", "%s: the planted trailing-byte reader passes", root.name)
		prefix := fuzzRoot{root.name, func(f fuzzForm, in []byte, rep *TableReport) ([]byte, []byte, bool) {
			if f == fuzzWire && len(in) < len(wire) && string(wire[:len(in)]) == string(in) {
				in = wire
			}
			return root.run(f, in, rep)
		}}
		require.Contains(t, fuzzNeighbours(prefix, wire), "strict prefix", "%s: the planted prefix reader passes", root.name)
	}
}
```

The file already imports `fmt`, `strings`, `testing` and testify `require`, and already
defines `fuzzRoot`, `fuzzForm`, `fuzzWire`, `fuzzEmpty`, `fuzzRoots`, `TableReport` and
`fuzzRoot.load`; the block adds no import and no helper. `Test@FUZZ@Neighbours` is a unit
test (no time, no socket, no network): it is the block's own reversed witness, failing both
planted readers on every root, so the oracle cannot silently become a no-op.

## The proof

Run on hetzner2 on 2026-10-17 (go1.26.6 linux/amd64, load average 30 to 50 on 16 cores),
the block applied by script to a throwaway worktree of each package (never committed), at
the head each package's fuzz test is at: nova-tools BASE `sprint/v15-schema-tables`
2bc0d585e for buswire and decidewire, the fuzz-<name> card head for the other nine, and
nova-sprint BASE `sprint/v15-schema-tables` 964d8c922 for its five. Per package: `go vet`,
the proof test and the seeds (`go test -count=1 -run '^(Test<F>Neighbours|<F>)$'`), and a
2 minute fuzz run (`go test -run '^$' -fuzz '^<F>$' -fuzztime=2m -parallel 8`), two packages
at a time.

| package | at | vet | proof test | seeds | 2 min fuzz | execs |
| --- | --- | --- | --- | --- | --- | --- |
| buswire | 2bc0d585e | ok | PASS 0.01 s | PASS 0.51 s | green | 127823 |
| configwire | 06284310a | ok | PASS 0.00 s | PASS 0.00 s | green | 5567654 |
| decidewire | 2bc0d585e | ok | PASS 0.36 s | PASS 1.06 s | green | 413234 |
| friendfilewire | c11a73b61 | ok | PASS 0.11 s | PASS 0.21 s | green | 488514 |
| hookwire | e27d8ef4b | ok | PASS 0.05 s | PASS 3.00 s | green | 237236 |
| ledgerwire | c0ffbcf1a | ok | PASS 0.04 s | PASS 0.06 s | green | 704157 |
| pongwire | f73518fe3 | ok | PASS 0.01 s | PASS 0.74 s | green | 454454 |
| releasewire | b86454079 | ok | PASS 0.17 s | PASS 0.25 s | green | 542905 |
| seatrecwire | d9a68bc95 | ok | PASS 0.08 s | PASS 0.19 s | green | 294069 |
| swarmwire | 444dcd00e | ok | PASS 0.05 s | PASS 0.13 s | green | 213404 |
| tablemodelwire | ca4a1e135 | ok | PASS 0.78 s | PASS 2.07 s | green | 459730 |
| donewire | 964d8c922 | ok | PASS 0.00 s | PASS 0.00 s | green | 13980050 |
| friendrowswire | 964d8c922 | ok | PASS 0.22 s | PASS 0.45 s | green | 276521 |
| oprowswire | 964d8c922 | ok | PASS 0.06 s | PASS 0.15 s | green | 726709 |
| snaprowswire | 964d8c922 | ok | PASS 0.57 s | PASS 1.23 s | green | 265156 |
| viewrowswire | 964d8c922 | ok | PASS 0.37 s | PASS 1.20 s | green | 522816 |

Planted bugs, each check run separately: the existing test with no block (none), the block
with the strict prefix half only (P), the one byte appended half only (E), and the block as
written (P+E). Seeds are `go test -run '^<F>$'`; the misses were then fuzzed for 2 minutes.

| plant | none | P | E | P+E |
| --- | --- | --- | --- | --- |
| configwire `MachineBeatLoad` trailing-bytes branch returns true, report restored (plant2) | missed (seeds, 2 min fuzz) | missed (seeds, 2 min fuzz) | caught on seeds: `MachineBeat: an accepted wire plus one byte 00 reads clean` | caught on seeds, same line |
| viewrowswire `StageStatLoad`, the same plant | missed (seeds, 2 min fuzz) | missed (seeds, 2 min fuzz) | caught on seeds: `StageStat: an accepted wire plus one byte 00 reads clean` | caught on seeds, same line |
| configwire `MachineBeatLoad` trailing-bytes check replaced by `panic(0)` (coordinator) | caught (no panic) | caught | caught | caught |
| `tableOpen` reading an over-large id count as 0; `Body()` clamping a nested length to the bytes left (coordinator, configwire and viewrowswire) | not failed in 30 s | not failed in 30 s | not failed in 30 s | not failed in 30 s |

The last row is the coordinator's measurement, not rerun here: whether either plant yields
any silent accept is not shown, so it is no evidence for or against the rule.

## Rulings

A ruling is owed for any package where a generated reader legitimately accepts a strict
prefix of its own wire, or reads its own wire with one byte appended clean. None was found:
both halves are green on the seeds and on a 2 minute fuzz run in all 16 packages.

The standing ruling: a generated reader found accepting either is a schema compiler bug,
filed against mas-bandwidth/schema with the root and the input in hex, and the
fuzz-<name>-prefix-oracle card for that package holds (`Verdict: HOLD`) until the compiler is
fixed and the package regenerated. The rule is never weakened per package, and no package
carries an exception list.
