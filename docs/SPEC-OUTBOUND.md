# nova-post — specification

**Revision 1, 2026-09-17.** Status: **proposed, not implemented. Documentation only.**
Glenn, 2026-09-17: *"The things we interface with outward are Bluesky, Ghost (the blog),
email and Discord. The house rule for outward-facing actions: they are Glenn's call every
time; a tool prepares, shows, and sends only on his explicit approval; nothing is posted,
mailed or published by a friend's judgment."* This page is that ruling as one verb family.
It is a sibling of [SPEC.md](SPEC.md), whose **Conventions** govern unchanged except where
stated. It obeys the lessons every path comes from a flag, one line per verb, a refusal is
exit 2 with a remedy, and tests run against fakes: no test here opens a socket to a real
provider, reads a real credential, or publishes anything.

**A credential reaches this tool only through `nova-secrets exec`**
([SPEC-SECRETS.md](SPEC-SECRETS.md)). `nova-post` never opens a secret file, never runs
`sops`, never takes a `--key`, and never has a plaintext to lose; it reads the one
environment variable the channel needs, and only because its launcher started it under
`nova-secrets exec … --only <NAME> --`. No value, fragment or **length** of a value ever
appears on an event line, in a refusal, or in the body. A missing variable is a refusal
naming the exec line, never a fall-back to the Keychain.

## The gate

One rule, and every outward action passes through it: **draft, show, approve, send.**
A tool may prepare a draft and render it; it may not release it. Release is Glenn's, given
as a bus receipt on his own lane that names the exact bytes by hash. **`approve` is not a
verb of this tool**: the approval is a receipt
([TERMINOLOGY.md](TERMINOLOGY.md#the-receipt-rule), [SPEC.md](SPEC.md)), and this tool
only reads it. So a line that can send still cannot choose to.

## The verbs

```
nova-post draft --channel <ghost|bsky|email|discord> --target <t> --file <body.md> \
                --drafts <dir> --allowlist <file> [--title <t>] [--link <url>] \
                [--digest <YYYY-MM-DD>] [--cairn <file>] [--fleet <file>]
nova-post show  --draft <hash> --drafts <dir>
nova-post send  --draft <hash> --approval <receipt-id> --drafts <dir> \
                --bus <dir> --allowlist <file>
nova-post version
nova-post help
```

`--drafts <dir>` is the draft store, required on all three verbs, **not guessed**; the
tool creates no directory. `--allowlist <file>` is required on `draft` and `send`: the
target must be named in it (below). `--file` is body markdown; `--title` and `--link` are
optional. `--digest` is email-only and mutually exclusive with `--file`; it renders the
day's cairn beats from `--cairn` and fleet numbers from `--fleet` (both required with it).

**`draft`** renders the payload for the channel once, by a fixed deterministic function,
and writes two files under `--drafts`: the payload bytes verbatim as `<hash>.post`, and a
one-line `<hash>.meta` holding `channel`, `target`, `title`, `link`, `created` and
`bytes`. `<hash>` is the lower-case SHA-256 of the payload bytes, and those bytes are
everything that can leave. Drafting performs no network call. It prints one line:

```
POST DRAFT OK hash=<sha256> channel=<c> target=<t> bytes=<n> drafts=<dir>
```

**`show`** writes to stdout **the exact payload bytes** and nothing else, so what is shown
is what is sent, byte for byte. Its receipt goes to stderr:

```
POST SHOW OK hash=<sha256> channel=<c> bytes=<n> drafts=<dir>
```

That stdout ownership is this tool's one deviation from Conventions, made for the same
reason `exec`'s OK line moved: the payload owns stdout from the first byte. Byte-stability
is tested: two `show` runs, and `show` against `send`'s bytes, are equal.

**`send`** refuses unless **all four** hold, and names the one that failed: the receipt
exists in `<bus>`; its sender is Glenn and no other; its body names **this** `<hash>`; and
its timestamp is **under 24 hours** old at send time. It then transmits the stored payload
and nothing freshly rendered, writes a `<hash>.sent` record, and is idempotent: a second
`send` of a sent hash prints the recorded result and makes no network call. One line:

```
POST OK channel=<c> id=<remote-id> url=<url> hash=<sha256> approval=<receipt-id> bytes=<n>
```

`id` and `url` are the provider's own identifiers as returned; `id=- url=-` when the
channel has neither (email). No other byte of the provider's response is ever printed.

## Approval, and why it is a receipt

Glenn's approval is a bus note on his own lane whose body carries exactly one line,
`APPROVE nova-post sha256=<hash>`, and `--approval` names that note's receipt id. A line
he pastes in chat is a way for a person to hand that note to `nova-bus`; the tool reads
the receipt and nothing else, so a chat line is not a second trust path. Approval is
**data and not a grant**: it names one hash, it expires, and it reaches no verb but `send`.
The 24-hour window is read from an injected clock in tests; there is **no `--now` flag**,
because a caller who can name the time can forge freshness.

## The channels

| `--channel` | the exact API | credential, via exec only | `--target` |
|---|---|---|---|
| `ghost` | Ghost Admin API: `POST /ghost/api/admin/posts/?source=html`, publish by `status`; images are referenced **by URL**, never uploaded | `GHOST_ADMIN_KEY` (Admin API key, JWT `id:secret`) | the site/tag the allowlist names |
| `bsky` | At Protocol: `com.atproto.repo.createRecord` with `app.bsky.feed.post`; a thread is `reply` refs to `root`/`parent`; `--link` becomes an `app.bsky.embed.external` link card | `BSKY_APP_PASSWORD` (identifier is the allowlisted handle, not a secret) | the handle |
| `email` | SMTP submission, or the provider's API where one is named; digest mode renders the day's cairn beats and fleet numbers | `SMTP_PASSWORD` (`SMTP_PASSWORD_BACKUP` when the sender is down) | a named list in the allowlist |
| `discord` | channel webhook; `fleet` is a read-only status mirror, `friends` is the ordinary channel | `DISCORD_FLEET_WEBHOOK`, `DISCORD_FRIENDS_WEBHOOK` | `fleet` or `friends` |

The Discord webhook URL **is** the credential and is sealed like one; if a store carries
`DISCORD_BOT_TOKEN` instead, this verb refuses rather than guess a second route. The two
webhook names are an owed addition to SPEC-SECRETS' credential table.

**Alt text is a refusal, not an option.** An image in the body whose markdown alt text is
empty is refused at draft time for `bsky`, because the AT Protocol's `alt` field is how a
reader who cannot see the image gets the post; a link card is rendered from `--link` with
its `title` and `description` taken from the body's own heading and first line.

**The allowlist** is a small file, one `channel<TAB>target` per line, read from
`--allowlist`; a target not in it is a refusal naming the file and the line to add. It is
the finite set the tool may ever address, and it is reviewed like `.sops.yaml`.

## Refusals

Every refusal is one line, on stderr, and **names the remedy**; exit 2 for an invocation
that could not run, exit 1 when the bus or a provider says NO.

| what is wrong | exit |
|---|---|
| a missing flag, an unreadable `--file`/`--cairn`/`--fleet`, a `--drafts` that is not a directory, a negative `--digest` day | 2 |
| `--digest` with `--file`, or without `--cairn`/`--fleet`; `--title`/`--link` on `discord` | 2 |
| the body names a secret: a path under a secrets store, a `*.key` file, `recovery.pub`, or any credential name on this page (`GHOST_ADMIN_KEY`, `SMTP_PASSWORD`, `BSKY_APP_PASSWORD`, `DISCORD_*_WEBHOOK`) | 2 |
| the credential variable is absent from this process: the exec line to run, with `--only <NAME>` | 2 |
| **no approval**: no receipt by that id in `--bus` | 1 |
| **an approval for another hash**, or a body naming a hash this draft is not | 1 |
| the receipt's sender is not Glenn | 1 |
| **a stale approval**: received 24 hours or more before the send | 1 |
| the target is not in `--allowlist`, or the allowlist file is absent | 1 |
| the provider answered 429, 5xx, or its transcript: the status, the `Retry-After` if given, and that **nothing was retried** | 1 |

The "names a secret" refusal prints the **line number and the shape**, never the matched
text, so the refusal cannot quote the secret back.

## Rate limits

Conservative caps, checked before the socket, one send at a time, and a `429` is **never**
retried automatically — an automatic retry of a send whose result was lost is a second
post. Exceeding a cap is exit 1 naming the cap and the wait:

- **ghost** — 1 post per 5s, 60 per hour per site, and the Ghost Admin API's own limit,
  whichever is lower.
- **bsky** — 1 record per 3s, 300 per day per handle, and the PDS's published limit;
  a thread is one draft and counts as its length.
- **email** — the provider's daily cap, one digest per recipient per day, and a
  `250/451` refusal passes through as a named failure, never a silent drop.
- **discord** — 1 message per 2s and 30 per minute per webhook, per Discord's own limit;
  the fleet mirror batches its status lines into one message.

## What is safe without approval

**Nothing outward.** No post, mail, publish or friend-channel message leaves without a
receipt naming its hash. The **one exception** is the Discord **fleet-status webhook**,
and only for lines the status verbs already print, unedited: the `OK` lines of
`nova-pulse status`, `nova-swarm status`, `nova-bus bus`, `nova-work status`,
`nova-merge status` and `nova-tokens usage`. That path takes no `--file` and no prose; any
line not matching one of those verbs' own grammar is refused, so the mirror cannot become
a posting channel by degrees. It is listed here by name because an exception not written
down is the hole. Everything else — the friends channel included — is draft, show,
receive, send.

## Tests this spec demands

Every test runs against **fake endpoints** (an `httptest` server per channel, a mock SMTP
listener) and throwaway drafts, approvals, allowlists and credentials; nothing reaches a
network or a real secret, and each test is proven able to fail by a mutation before it is
trusted.

1. `TestSendWithoutApprovalIsRefused` — a valid draft and no receipt is exit 1, and the
   fake endpoint records **zero** requests.
2. `TestApprovalForAnotherHashIsRefused` — a receipt naming a different hash is exit 1,
   zero requests.
3. `TestShownBytesEqualSentBytes` — `show`'s stdout and the bytes the fake endpoint
   received are **equal**, for all four channels.
4. `TestStaleApprovalIsRefused` and `TestApprovalNotFromGlennIsRefused` — 24h on the dot
   and any other sender are exit 1, zero requests.
5. `TestBodyNamingASecretIsRefused` — each denied shape above is exit 2, and the output
   does not contain the matched text.
6. `TestTargetNotInAllowlistIsRefused` — exit 1, zero requests.
7. `TestDigestForFixtureDayMatchesGolden` — a fixture cairn and fleet produce bytes equal
   to the committed golden, and two renders are equal.
8. `TestSendIsIdempotent` — the second send of one hash makes no second request.
