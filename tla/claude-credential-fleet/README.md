# Claude credential fleet model

This finite model is a partial design check for the credential-seat registry
and setup admission. `SourceBindings` and `TargetBindings` mirror the tagged
account/source-seat and account/profile/target-seat records parsed by
`internal/fleet/credential_seats.go`. The fixture uses symbolic hosts: two
accounts share one source host; one account profile spans two target hosts; a
second account has a separate profile on one of those target hosts.

The model checks account, source-seat, target-seat, profile and host binding;
separate token generations; owner issuance and sealing; delivery to each
target; process restart; native-use proof; and revocation only after every
target has the current generation, has passed a fresh native turn, and no
process retains the old generation. It includes wrong-source, wrong-target,
cross-account, early-revocation and revoked-generation-replay witnesses.
`PartialFleetDistribution` is reachable during rotation; the corresponding
case deliberately checks that `NoPartialFleet` is violated.

The only credential data in the modeled state is an opaque `(account,
generation)` label. The model has no credential values, refresh tokens, keys,
cryptographic material, real expiry times or stdout. Provider acceptance and
expiry are nondeterministic external events. Native-use proof is a model event,
not evidence that any real user authenticated or that a provider accepted an
account. Fleet retirement is separate from provider-side revocation, which is
not modeled.

Refresh custody is represented as a capability distinction. The local OAuth
fixture refreshes only at its declared source. The subscription-token fixture
has no shared refresh owner; only explicit owner issuance may create another
generation. This does not assume that a refresh grant can be copied or used on
another host, nor does it assert that an account currently has usable auth.

This coverage is partial. It does not model key derivation or encryption,
secret-store bytes, real provider entitlement or expiry, login, browser/device
consent, manifest file races, native process isolation, durable filesystem
crashes, account identity confirmation, or production activation. These
implementation boundaries remain outside this model and require
separate evidence. Source-level tests and reviews do not establish live
provider entitlement, user consent or production activation.

The eight declared cases have measured records in `tla/RUNS.tsv`. Their full
per-case logs and counterexample traces are retained in the external release
evidence for this run. Earlier failed attempts and the superseded first model
measurement are preserved separately from the product source.
