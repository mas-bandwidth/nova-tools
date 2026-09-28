#!/usr/bin/env python3
"""The executable named-test gate of nova-tools#3174 (rev 6), used by every part.

A gate passes only when the captured command exited 0, every required name has
exactly one top-level PASS line, and the whole output holds no FAIL or SKIP
test status -- an indented Go subtest such as `    --- SKIP: TestName/nested`
included. An absent name, a repeated PASS, `[no tests to run]`, a nested skip,
a fail or a nonzero command exit cannot pass.

    check-named-tests.py --self-test
    check-named-tests.py --style lisp|go --command-exit N --output FILE --names 'a|b'

Lisp lines are the suite's own (tests/harness.lisp): `TEST <name> PASS ...` and
`TEST <name> FAIL ...`. Go lines are `go test -v`'s: `--- PASS: <name> (...)`
at column 0 is top level; any `--- FAIL:` or `--- SKIP:`, at any indent, and a
`FAIL` package line are failures.
"""

import re
import sys

LISP_STATUS = re.compile(r"^(\s*)TEST (\S+) (PASS|FAIL|SKIP)(?: |$)")
GO_STATUS = re.compile(r"^(\s*)--- (PASS|FAIL|SKIP): (\S+)")
GO_FAIL_LINE = re.compile(r"^FAIL(?:\s|$)")


def check(style, command_exit, text, names):
    """Return the list of problems; empty means the gate passes."""
    problems = []
    if command_exit != 0:
        problems.append("command exit %d, not 0" % command_exit)
    passes = {}
    for line in text.splitlines():
        if style == "lisp":
            m = LISP_STATUS.match(line)
            if not m:
                continue
            indent, name, status = m.group(1), m.group(2), m.group(3)
            if status != "PASS":
                problems.append("status %s: %s" % (status, line.strip()))
            elif indent == "":
                passes[name] = passes.get(name, 0) + 1
        else:
            if GO_FAIL_LINE.match(line):
                problems.append("status FAIL: %s" % line.strip())
                continue
            m = GO_STATUS.match(line)
            if not m:
                continue
            indent, status, name = m.group(1), m.group(2), m.group(3)
            if status != "PASS":
                problems.append("status %s: %s" % (status, line.strip()))
            elif indent == "":
                passes[name] = passes.get(name, 0) + 1
    for name in names:
        n = passes.get(name, 0)
        if n == 0:
            problems.append("absent: %s has no top-level PASS line" % name)
        elif n > 1:
            problems.append("repeated: %s has %d top-level PASS lines" % (name, n))
    return problems


def self_test():
    """One present and one absent name, and nested-skip fixtures, in both
    formats. Prints exactly `CHECK named-test-gate PASS` only when the present
    fixture passes and the absent and nested-skip fixtures fail."""
    lisp_ok = "TEST alpha PASS spec=x y\nNOVA-WORK SLICE1 total=1 pass=1 fail=0\n"
    lisp_skip = "TEST alpha PASS spec=x y\n  TEST alpha/nested SKIP spec=x y\n"
    go_ok = "=== RUN   TestAlpha\n--- PASS: TestAlpha (0.00s)\nPASS\nok  \tpkg\t0.1s\n"
    go_skip = ("=== RUN   TestAlpha\n=== RUN   TestAlpha/nested\n"
               "--- PASS: TestAlpha (0.00s)\n    --- SKIP: TestAlpha/nested (0.00s)\nPASS\n")
    cases = [
        ("lisp present", check("lisp", 0, lisp_ok, ["alpha"]) == []),
        ("lisp absent", check("lisp", 0, lisp_ok, ["alpha", "beta"]) != []),
        ("lisp nested skip", check("lisp", 0, lisp_skip, ["alpha"]) != []),
        ("lisp nonzero exit", check("lisp", 1, lisp_ok, ["alpha"]) != []),
        ("go present", check("go", 0, go_ok, ["TestAlpha"]) == []),
        ("go absent", check("go", 0, go_ok, ["TestAlpha", "TestBeta"]) != []),
        ("go nested skip", check("go", 0, go_skip, ["TestAlpha"]) != []),
        ("go no tests to run", check("go", 0, "testing: warning: no tests to run\nPASS\n", ["TestAlpha"]) != []),
        ("go repeated", check("go", 0, go_ok + go_ok, ["TestAlpha"]) != []),
    ]
    bad = [label for label, held in cases if not held]
    if bad:
        for label in bad:
            print("CHECK named-test-gate SELF-TEST FAIL %s" % label)
        return 1
    print("CHECK named-test-gate PASS")
    return 0


def usage(why):
    sys.stderr.write("check-named-tests.py: %s\n" % why)
    sys.stderr.write(__doc__)
    return 2


def main(argv):
    if argv == ["--self-test"]:
        return self_test()
    opts = {}
    i = 0
    while i < len(argv):
        key = argv[i]
        if key not in ("--style", "--command-exit", "--output", "--names") or i + 1 >= len(argv):
            return usage("unknown or incomplete argument %r" % key)
        if key in opts:
            return usage("%s given twice" % key)
        opts[key] = argv[i + 1]
        i += 2
    for key in ("--style", "--command-exit", "--output", "--names"):
        if key not in opts:
            return usage("missing %s" % key)
    style = opts["--style"]
    if style not in ("lisp", "go"):
        return usage("--style must be lisp or go")
    if not re.fullmatch(r"-?[0-9]+", opts["--command-exit"]):
        return usage("--command-exit must be an integer")
    names = opts["--names"].split("|")
    if any(n == "" for n in names) or len(set(names)) != len(names):
        return usage("--names must be distinct non-empty names joined by |")
    try:
        with open(opts["--output"], encoding="utf-8", errors="replace") as f:
            text = f.read()
    except OSError as e:
        return usage("cannot read --output: %s" % e)
    problems = check(style, int(opts["--command-exit"]), text, names)
    if problems:
        for p in problems:
            print("CHECK named-test-gate FAIL %s" % p)
        return 1
    print("CHECK named-test-gate PASS")
    for name in names:
        print("CHECKED %s PASS" % name)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
