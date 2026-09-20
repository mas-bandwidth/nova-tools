RESULT: s17mr-1076 sha=<no base tree>

**CANNOT READ**

The base repo tree (`repo/`) and the bundle at `/tmp/schema14-ftf.bundle` are both absent. There is no way to verify context lines, read the law docs, or confirm the files touched by the diff. The diff itself is truncated at 6000/8987 bytes in `make/negative-controls.json`: the entry for `"base-conformance-cpp"` is cut off mid-`why` string (`"reg"`) and the `actions` field (which determines CI gate reachability) is entirely invisible. Without the full entry and without the base tree, no verdict on correctness is possible.