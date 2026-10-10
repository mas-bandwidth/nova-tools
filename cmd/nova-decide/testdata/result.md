head: 3f2a9c1d0b7e
branch: sprint/c1.w1.g1.e1
verdict: not-done
gate: go test ./pkg/decide/... (FAIL: TestRenamedHelperKeepsItsName)
output: -
report: the rename is in, but TestRenamedHelperKeepsItsName is red: the helper's old name is still asserted in its table
