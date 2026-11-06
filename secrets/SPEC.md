# nova-secrets: tests and models

The [guide](README.md) is how to use the tool. This page is for changing it.
Build and test commands for every tool are in [TESTING.md](../docs/TESTING.md).

Unit tests stand in for sops, age-keygen, git and the process replacement. The
fake sops seals a file to the recipients it is given (the rule's keys, or the
`--age` list when `--config` points elsewhere), refuses to open a file whose
recipients do not include the key, and records every call, so a test can show
that no value reached an argument, an environment other than the command's, or
any output, and that `grant` passed sops `--config /dev/null` and exactly the
two pinned keys. A test can also make the fake seal to one more key, to show
that `set` and `grant` refuse what sops returned before writing it. Functional
tests (`-tags functional`) run the built binary on real sops, age-keygen and
git in the test's own directory, and are skipped where those are missing.
`TestARealFleet` deletes the recovery private key straight after `init`, fills
a machine's seat by `grant`, shows that the control key cannot open the grants
file, and then edits the store as a pusher would: a rule with `key_groups` and
a catch-all naming a stranger's key (the grant still reaches only the pinned
keys, and the stranger's key opens nothing), an anchor in `.sops.yaml`, a
replaced rule key for `grant` and for `set` (the seat file keeps its
recipients), a symlinked grants file, and a name planted in a grants file, each
refused.

The model in `secrets/tla/Recipients.tla` covers who can open a grants file.
Its state is what any pusher can edit in the store (each seat's rule, the rule's
seat key, `recovery.pub`, which file a seat's grants path really is, and the
names a grants file holds), the pins and ledger on the control node, and the
seat and grants files written. Tamper actions change each of those at any time;
`Pin` stands for join reading the machine's key over SSH; `Set`, `Grant` and
`Read` are the tool; a pusher can also re-seal a grants file with other values
(`TamperFile`), and `Pin` can pin the control node's own key by mistake. No
action uses the recovery key. It checks that a grants
file reaches only its machine's key and the recovery key, that the recovery key
opens every one, that the control key opens none, that a seat's own file
reaches only its own key and the recovery key, that `grant` fills only names it
was asked for, that no seat reads another seat's file, and that every machine
whose store entries are untouched, and whose pin is its own key, comes to hold
the fleet value. Nine reversed witnesses each break one of these, one per way
the store or a wrong pin could have steered a write: the granting key added as a recipient, sealing to what the rule names
(which covers a rule widened by `key_groups`, `pgp` or YAML anchors), trusting
the rule's seat key instead of the pin, trusting the store's `recovery.pub`,
reading a seat file through a symlink, keeping the names the store's grants file
holds instead of the ledger's, `set` sealing to what the rule names, a grant
that trusts any file present instead of the one it wrote, and a grant through
a pin that is the control node's own key. The
model has no state for where the pins live (the code refuses a pin directory in
the store or in any git work tree) and no tamper of a file's contents (a pusher
can re-seal any file to a seat's public key; `grant` rewrites a grants file whose
bytes are not the ones it wrote, which `TamperFile` and the `trustfile` witness
cover, and nothing can repair a seat's own file). The sequence write, ledger,
commit and a failed commit are not modelled; a unit test covers them. The model does not cover the order of rules in
`.sops.yaml`, which matters only to `set`; unit tests cover that.

Run the model with TLC, with Java on PATH and the jar pinned in
[tests/tla/tla2tools.sha256](../tests/tla/tla2tools.sha256), at an absolute path:

```sh
tests/tla/check.sh /opt/tla/tla2tools.jar secrets/tla
```

Each line starts with `ok` when the case came out as `secrets/tla/CASES.tsv` says.
