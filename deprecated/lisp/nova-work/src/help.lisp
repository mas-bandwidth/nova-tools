;;;; help.lisp --- bounded family/verb help and machine discovery with a
;;;; schema hash: nova-work criterion E08-F01-02 (docs/roadmaps/nova-work.sexp,
;;;; feature E08-F01, "Provide bounded family/verb help and machine discovery
;;;; with schema hash").
;;;;
;;;; ONE SCHEMA. *VERB-SCHEMA* below is the verbs block of docs/SPEC-WORK.md
;;;; (the fenced block at :2258-2348 that ends `nova-work help`), every form of
;;;; every verb, verbatim, with its continuation lines joined by one space, and
;;;; each verb placed in one family of the spec's own operations table ("data
;;;; or action | required coordinator operations", docs/SPEC-WORK.md:2923-2936).
;;;; The criterion test reads the spec's block at test time and fails when the
;;;; two disagree, so the schema cannot drift from the block silently.
;;;;
;;;; BOUNDED. A reader learns a verb without carrying the manual in context:
;;;;   `help`                -- one HELP FAMILY row per family (name, verb
;;;;                            count, what it covers); no usage line at all;
;;;;   `help <family>`       -- one HELP VERB row per verb of that family;
;;;;   `help <verb>`         -- that verb's forms, one HELP USAGE row each;
;;;;   `help --machine`      -- one DISCOVERY VERB row per verb, every field
;;;;                            named, with the digest of that verb's forms.
;;;; Every view prints at most --max rows (default 32, at most 256) from row
;;;; --after (default 0), and its last line says how many it left, `more=`,
;;;; and where to continue, `next=`: a page is never cut silently.
;;;;
;;;; SCHEMA HASH. Every answer's last line carries `schema=<sha256>` of the
;;;; schema's canonical bytes, the same on every page and in every view, so a
;;;; reader holding one page can tell whether the next came from the same
;;;; schema. Refusing a stale one is E08-F01-03 and is not done here.
;;;;
;;;; NOT WIRED TO THE SOCKET. The session's request line is one line in and one
;;;; line out (src/request-line.lisp); a help answer is several rows, so the
;;;; framed protocol carries it when it lands. HELP-ANSWER is the pure answer,
;;;; (values OK LINES EXIT), as the other slice verbs are.

(in-package #:nova-work)

(defparameter *help-families*
  '(("durability" "session and durability")
    ("reversal" "reversible mistakes")
    ("structure" "work structure")
    ("scope" "scope and dependencies")
    ("assignment" "assignments and execution")
    ("completion" "evidence and completion")
    ("roadmaps" "roadmaps")
    ("friends" "friends and CONFIG")
    ("active" "ACTIVE")
    ("models" "models and prices")
    ("queries" "queries and operations")
    ("meta" "the tool itself"))
  "The families, in the order of the spec's operations table
(docs/SPEC-WORK.md:2923-2936), each with that table's own heading. `meta` holds
`version` and `help`, which the table does not list. No family is spelled like
a verb, so `help <word>` never has two readings.")

(defparameter *verb-schema*
  '(
    ("session start" "durability"
     "nova-work session start  --session <path> --as <name> --file <path-in-repo> --journal <path> --cache <path> --repo <path> --remote <name> --branch <name> --max-bytes <n> --max-depth <n> --max-nodes <n> --every <duration> --skew <duration> --clip-every <duration> --clip-after <n> --retain <duration> --savepoint-every <duration> --savepoint-after <n> --max-frame-bytes <n> --silence-ping <duration> --index-cache <n> --page-bytes <n> --page-records <n> [--closed-window <duration>] [--render-root <root-id>=<owner/name>:<directory> ...] [--resolver <scheme>=<command> ...] --git-timeout <seconds> [--attempts <n>] [--repair] [--foreground] [--max <n>] [--now <stamp>]")
    ("session export" "durability"
     "nova-work session export (--session <path> | --journal <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) --into <path>"
     "nova-work session export (--session <path> | --snapshot <path> --cache <path>) --state --at <revision> --closed-history <none|all|range> [--from <stamp> --to <stamp>] --into <new-directory> --max-bytes <n> --max-depth <n> --max-nodes <n> --max-output-bytes <n>   (a long operation under --session; one finite process under --snapshot)")
    ("state load" "durability"
     "nova-work state load     --from <export-directory> --into <new-readonly-session> --max-bytes <n> --max-depth <n> --max-nodes <n>   (isolated and read-only: starts no daemon, takes no ownership)")
    ("session replay" "durability"
     "nova-work session replay --session <path> --from <path> --as <name> [--max <n>]")
    ("session status" "durability"
     "nova-work session status --session <path>")
    ("session stop" "durability"
     "nova-work session stop   --session <path> --git-timeout <seconds> [--attempts <n>] [--no-clip]")
    ("session handoff" "durability"
     "nova-work session handoff --session <path> --to <name> --git-timeout <seconds> [--attempts <n>]")
    ("operation status" "queries"
     "nova-work operation status  --session <path> --id <id>")
    ("operation list" "queries"
     "nova-work operation list    --session <path> [--max <n>]")
    ("operation wait" "queries"
     "nova-work operation wait    --session <path> --id <id> --timeout <duration> [--after <cursor>]")
    ("operation cancel" "queries"
     "nova-work operation cancel  --session <path> <write flags> --id <id> --reason <text>")
    ("savepoint list" "durability"
     "nova-work savepoint list     --session <path> [--max <n>]")
    ("savepoint create" "durability"
     "nova-work savepoint create   --session <path> --as <name> --reason <text>")
    ("savepoint verify" "durability"
     "nova-work savepoint verify   --session <path> --id <id>")
    ("savepoint restore" "durability"
     "nova-work savepoint restore  --savepoint <path> --into <path> --max-bytes <n> --max-depth <n> --max-nodes <n>   (isolated and read-only: takes no ownership, dispatches nothing, replays no message)")
    ("savepoint compare" "durability"
     "nova-work savepoint compare  --savepoint <path> --against (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) [--max <n>]")
    ("undo-plan" "reversal"
     "nova-work undo-plan      --session <path> --request <id> [--max <n>]")
    ("undo" "reversal"
     "nova-work undo           --session <path> <write flags> --request-of <id> --reason <text>")
    ("redo-plan" "reversal"
     "nova-work redo-plan      --session <path> --request <id> [--max <n>]")
    ("redo" "reversal"
     "nova-work redo           --session <path> <write flags> --request-of <id> --reason <text>")
    ("friend" "friends"
     "nova-work friend         --session <path> <write flags> (--register <name> | --retire <name> | --role <name>=<role>[:<scope>] | --participation <name>=<yes|no|withdrawn> | --capability <name>=<capability-id> --group <child|swarm|local|one-shot> --limit <n> | --limit <name>=<n>) --reason <text>")
    ("config" "friends"
     "nova-work config         --session <path> (--request <name> --base <hash|-> | --export <name> --into <path> | --intake --from <path> <write flags>) [--max <n>]")
    ("model" "models"
     "nova-work model          --session <path> <write flags> (--register <id> --provider <name> --route <text> --billing <metered|subscription|local|unknown> | --rate <id>=<pricing-id> --effective <stamp> --source <pointer> | --evidence <id> --task-class <label> --result <pointer> --samples <n>) --reason <text>")
    ("observe" "active"
     "nova-work observe        --session <path> <write flags> --friend <name> (--state <awake|resting|unavailable|unconfirmed> --source <pointer> | --attempt <id> --observed-model <id> --bench <name> --usage <pointer>) --reason <text>")
    ("goal set" "active"
     "nova-work goal set       --session <path> <write flags> --expect <rev> [--scope <scope>] (--goal <node-id> | --clear) --reason <text>   (--expect required here; --scope defaults to the caller's --as)")
    ("goal show" "active"
     "nova-work goal show      (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --as <name> [--scope <scope>] --max <n>")
    ("goal update" "active"
     "nova-work goal update    --session <path> <write flags> --expect <rev> [--scope <scope>] (--progress <text> [--evidence <pointer> --criterion <id> --against <sha>] | --blocked-by <node-id> --reason <text> | --stop --reason <text>)   (writes on the current goal node of the scope and on no other node)")
    ("machine" "friends"
     "nova-work machine        --session <path> <write flags> (--register <id> --name <text> --owner <name> --connect <ref> --role <build|test|profile> ... | --retire <id> | --permit <id>=<kind> | --exclude <id>=<kind> | --limit <id> <key>=<n|n,n,...> | --fact <id> <key>=<value> --declared-by <name>) --reason <text>")
    ("route" "models"
     "nova-work route          --session <path> <write flags> (--register <id> --provider <name> --endpoint <url> --key-location (:path \"<path>\"|:env \"<name>\") --plan <flat|metered|free|local> [--cost-per-mtok <n>] --capabilities <text=yes|no,code=yes|no,tool-calls=yes|no> --owner <name> | --retire <id> | --probe <id> --card <pointer> --pass <true|false|absent> [--wall <duration> --usd <amount>] --source <pointer>) --reason <text>")
    ("offer" "assignment"
     "nova-work offer          --session <path> <write flags> --node <id> --offer <offer-id> --to <name> --profile <capability-id>@<config-revision> --attempt <attempt-id> --generation <n> --request-ref <opaque-id> --payload <pointer> --payload-sha256 <hex> --reserve <slots> --until <stamp> [--requested-model <model-id>] [--predecessor-offer <offer-id> --predecessor-attempt <attempt-id>] [--reason <text>]")
    ("profile" "friends"
     "nova-work profile        --session <path> <write flags> (--write <name> --model <id> --harness <id> --work-type <label> --pointer <path> --policy <revision> [--evidence <pointer>] [--expiry <stamp>] [--owner <name>] | --edit <name> (--pointer <path> | --policy <revision> | --evidence <pointer> | --expiry <stamp> | --owner <name>)) --reason <text>   (an edit re-pins the digest when it changes --pointer; a manager session selects one by name at start and never swaps it mid-session)")
    ("acknowledge" "assignment"
     "nova-work acknowledge    --session <path> <write flags> --offer <offer-id> --reply <receipt-id> --stage <received|accepted> --provenance <pointer> --provenance-sha256 <hex> [--by <duration|stamp> --default <release|extend-once|escalate:<name>>] [--observed-model <model-id>] [--bench <name>] [--execution <handle>] [--reason <text>]   (--stage accepted: --by and --default, required, create-if-needed; --stage received: both exit 2)")
    ("decline" "assignment"
     "nova-work decline        --session <path> <write flags> --offer <offer-id> --reply <receipt-id> --provenance <pointer> --provenance-sha256 <hex> [--reason <text>]")
    ("execution pause" "assignment"
     "nova-work execution pause     --session <path> <write flags> (--node <id> | --repo <owner/name> | --all) --reason <text>")
    ("execution stop" "assignment"
     "nova-work execution stop      --session <path> <write flags> (--node <id> | --repo <owner/name> | --all) --reason <text>")
    ("execution resume" "assignment"
     "nova-work execution resume    --session <path> <write flags> --control <id> --action <release-hold|resume-workers> --reason <text>")
    ("execution correct" "assignment"
     "nova-work execution correct   --session <path> <write flags> --node <id> --instructions <pointer> --sha256 <hex> --reason <text>")
    ("execution reconcile" "assignment"
     "nova-work execution reconcile --session <path> <write flags> --control <id> --from <manifest-id> --reason <text>   (a content identity, never a local path)")
    ("execution status" "assignment"
     "nova-work execution status    --session <path> --control <id> [--max <n>]")
    ("clip" "durability"
     "nova-work clip           --session <path> --as <name> --git-timeout <seconds> [--attempts <n>] [--max <n>] [--now <stamp>]")
    ("check" "roadmaps"
     "nova-work check          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) [--max <n>]")
    ("verify" "completion"
     "nova-work verify         --session <path> (--offline | --max-fetch <n> --fetch-timeout <seconds>) [--node <id>] [--max <n>]")
    ("query" "queries"
     "nova-work query          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --ask <kind> --branch <open|closed|root> (--ask is one of: done, remaining, who, percent, size, stream, under, stale, handoffs, roadmap, friends, models, ready, fleet, routes, reports) [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>] [--axis <member>] [--for <workload-kind>] [--class <card-class>] [--since <revision>] [--at <revision>] [--from <stamp>] [--to <stamp>] [--after <cursor>] [--page-budget <n>] [--max <n>] [--order <discovery|priority>] (who and stale: --window <duration>, required; percent: --axis <member>, required on a matrix and refused on a zero- or one-axis roadmap; ready: --order, optional, discovery by default; --order priority on any other ask is exit 2; --branch closed and --branch root: --from and --to, required, and refused under --branch open; who, stale, handoffs and reports: --branch open only, the other two exit 2; reports: --since <revision>, required (SPEC-AHEAD: #854); fleet: --for optional, --node names a machine id, and --for with --node on a member that excludes the kind is refused; routes: --class optional, the card class whose ordered route list the projection emits, cheapest first; without it the whole registry is listed)")
    ("render" "roadmaps"
     "nova-work render         --session <path> --view <roadmap-id> (--chat [--projection <id> | --row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...] | --projection <id> (--file | --check)) [--at <revision>]")
    ("node add" "structure"
     "nova-work node add       --session <path> <write flags> --id <id> --type <work-set|epic|feature|task> (--under <parent-id> | --under-root open --repo <owner/name>) [--title <text>] [--category <label>] [--required <true|false>] [--acceptance <id:kind:subject:predicate> ...] [--link <text> ... | --links-empty | --clear-links] [--private <true|false>] [--version <text>] --reason <text>   (--type roadmap is exit 2 naming `roadmap create`)")
    ("node edit" "structure"
     "nova-work node edit      --session <path> <write flags> --node <id> (--title <text> | --clear-title | --category <label> | --clear-category | --link <text> ... | --links-empty | --clear-links | --private <true|false> | --clear-private | --version <text> | --clear-version) ... --reason <text>")
    ("node move" "structure"
     "nova-work node move      --session <path> <write flags> --node <id> --from <parent-id> --under <parent-id> --reason <text>")
    ("node remove" "structure"
     "nova-work node remove    --session <path> <write flags> --node <id> --reason <text>")
    ("node require" "structure"
     "nova-work node require   --session <path> <write flags> --node <id> --to <true|false> --reason <text>")
    ("decompose" "structure"
     "nova-work decompose      --session <path> <write flags> --node <id> --into <id,...> --acceptance <child-id:id:kind:subject:predicate> ... --reason <text>")
    ("accept" "completion"
     "nova-work accept         --session <path> <write flags> --node <id> (--add <id:kind:subject:predicate> | --remove <id>) --reason <text>")
    ("source" "completion"
     "nova-work source         --session <path> <write flags> --node <id> --to <sha> --reason <text>")
    ("dep" "scope"
     "nova-work dep            --session <path> <write flags> --node <id> (--add <id> | --remove <id>) --reason <text>")
    ("axis" "roadmaps"
     "nova-work axis           --session <path> <write flags> --roadmap <id> --axis <id> (--add <member> | --remove <member>) --reason <text>")
    ("roadmap create" "roadmaps"
     "nova-work roadmap create --session <path> <write flags> --id <id> --under <parent-id> [--title <text>] --row-kind <feature|epic|work-set> --aggregation <required-members|all-members|leaves> --completion-policy all-required-features (--axes-none | --axis-id <id> ...) [--permit-root <root-id> ...] --reason <text>")
    ("roadmap configure" "roadmaps"
     "nova-work roadmap configure --session <path> <write flags> --roadmap <id> [--row-kind <kind>] [--aggregation <policy>] [--completion-policy all-required-features] [--axes-none | --axis-id <id> ...] [--permit-root <root-id> ... | --roots-empty] --reason <text>")
    ("roadmap row" "roadmaps"
     "nova-work roadmap row    --session <path> <write flags> --roadmap <id> (--add <member> | --remove <member>) --reason <text>   (axisless roadmaps only)")
    ("roadmap projection" "roadmaps"
     "nova-work roadmap projection --session <path> <write flags> --roadmap <id> (--add <id> --root <root-id> --repo <owner/name> --path <relative-path> --start <marker> --end <marker> --policy markdown-table [--row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...] | --remove <id>) --reason <text>")
    ("prioritise" "scope"
     "nova-work prioritise     --session <path> <write flags> --node <id> (--set <rank> | --clear) [--context <self|subtree>] --reason <text>")
    ("cell" "roadmaps"
     "nova-work cell           --session <path> <write flags> --roadmap <id> --coord <member,member> (--ref <id|-> | --out-of-scope | --in-scope) --reason <text>   (--ref - clears the mapping)")
    ("responsible" "assignment"
     "nova-work responsible    --session <path> <write flags> --node <id> --to <name> --reason <text>")
    ("take" "assignment"
     "nova-work take           --session <path> <write flags> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>>")
    ("heartbeat" "assignment"
     "nova-work heartbeat      --session <path> <write flags> --node <id> --evidence <pointer>"
     "nova-work heartbeat      --session <path> <write flags> --allocation <id> --generation <n>   (allocation heartbeat: --allocation names the allocation id returned by take, --generation is the machine generation)")
    ("release" "assignment"
     "nova-work release        --session <path> <write flags> --node <id> [--handed <name> --by <duration|stamp> --default <release|extend-once|escalate:<name>>]"
     "nova-work release        --session <path> <write flags> --allocation <id> --generation <n> [--handed <name>]   (allocation release: --allocation names exactly one allocation, --generation is the machine generation; frees that allocation's slot only)")
    ("attest" "completion"
     "nova-work attest         --session <path> <write flags> --node <id> --criterion <id> --result <pointer> --against <sha>")
    ("attempt" "assignment"
     "nova-work attempt        --session <path> <write flags> --node <id> --model <name> --bench <name> --result <pointer> [--usage <pointer>]")
    ("evidence" "completion"
     "nova-work evidence       --session <path> <write flags> --node <id> --pointer <pointer> --criterion <id> --against <sha> [--attempt <id>]")
    ("state" "completion"
     "nova-work state          --session <path> <write flags> --node <id> --to <state> (--evidence <event-id> ... | --reason <text>) [--blocked-by <id>]")
    ("correct" "completion"
     "nova-work correct        --session <path> <write flags> --node <id> --reason <text>")
    ("event" "scope"
     "nova-work event          --session <path> <write flags> --kind <baseline|discovery|defer|cancel|reopen|supersede> --node <id> --reason <text> [--member <id,...>] [--superseded-by <id>] [--evidence <pointer>] (baseline and discovery: --member, required, and --kind discovery on a :roadmap is exit 2 naming `axis --add`; supersede: --superseded-by, required; cancel: --evidence <pointer>, required, and a note: pointer IS admitted here, because it evidences a stopped worker and never a done; --member on any other kind is exit 2)")
    ("report" "active"
     "nova-work report         --session <path> <write flags> --act <launched|stopped|other> --subject <node|machine|friend|route|offer|external>:<text> --what <text> --acted-at <stamp> --instead-of <text|-> --reason <text>   (SPEC-AHEAD: #854; records a hand act whose effect lies outside the tree and changes no tree state)")
    ("version" "meta"
     "nova-work version")
    ("help" "meta"
     "nova-work help")
    )
  "Every verb of docs/SPEC-WORK.md's verbs block (:2258-2348), in the block's
order: (VERB FAMILY FORM ...), each FORM one verbatim line of the block with its
continuation lines joined. Generated from the block; the criterion test
e08-f01-02-schema-is-the-spec-verbs-block reads the block and pins it.")

(defparameter *help-default-max* 32
  "Rows a help or discovery page prints when --max is not given.")

(defparameter *help-max-max* 256
  "The largest --max a page accepts; a larger one is refused, never clamped.")

;;; ------------------------------------------------------------------
;;; The schema hash.
;;; ------------------------------------------------------------------

(defun %help-schema-bytes ()
  "The canonical text the schema hash is taken over: a version line, one line
per family (name TAB heading), then one line per verb (verb TAB family TAB each
form), newline-terminated, in schema order."
  (with-output-to-string (s)
    (write-string "nova-work-help-schema-v1" s) (terpri s)
    (dolist (f *help-families*)
      (format s "family~C~A~C~A~%" #\Tab (first f) #\Tab (second f)))
    (dolist (v *verb-schema*)
      (format s "verb~C~A~C~A~{~C~A~}~%" #\Tab (first v) #\Tab (second v)
              (loop for form in (cddr v) append (list #\Tab form))))))

(defun help-schema-hash ()
  "The lowercase hex SHA-256 of the schema's canonical bytes."
  (sha256-hex (%help-schema-bytes)))

(defun %verb-forms-hash (entry)
  "The lowercase hex SHA-256 of one verb's forms, newline-joined."
  (sha256-hex (format nil "~{~A~%~}" (cddr entry))))

;;; ------------------------------------------------------------------
;;; Reading the request.
;;; ------------------------------------------------------------------

(defun %help-one-line (text)
  "TEXT with every control character replaced, so a row is one line."
  (map 'string (lambda (c) (let ((code (char-code c)))
                             (if (or (< code 32) (= code 127)) #\? c)))
       text))

(defun %help-fail (topic reason)
  (values nil
          (list (format nil "HELP FAIL topic=~A: ~A; run: nova-work help"
                        (if (zerop (length topic)) "-" (%echo-safely topic))
                        reason))
          2))

(defun %help-count (flag value least most)
  "VALUE as a decimal count between LEAST and MOST, or a refusal reason string."
  (cond ((or (null value) (zerop (length value)))
         (format nil "~A wants a count" flag))
        ((notevery #'digit-char-p value)
         (format nil "~A wants a decimal count, got ~A" flag (%echo-safely value)))
        (t (let ((n (parse-integer value)))
             (if (or (< n least) (and most (> n most)))
                 (if most
                     (format nil "~A is ~D to ~D, got ~D" flag least most n)
                     (format nil "~A is at least ~D, got ~D" flag least n))
                 n)))))

(defun %help-page (rows after max)
  "(values PAGE MORE NEXT) for ROWS from AFTER, at most MAX of them."
  (let* ((total (length rows))
         (start (min after total))
         (end (min total (+ start max)))
         (more (- total end)))
    (values (subseq rows start end) more (if (zerop more) "-" (princ-to-string end)))))

;;; ------------------------------------------------------------------
;;; The answer.
;;; ------------------------------------------------------------------

(defun help-answer (request)
  "Answer ONE help REQUEST line -- `help [<family>|<verb>] [--max <n>] [--after
<n>]` or `help --machine [--max <n>] [--after <n>]` -- as (values OK LINES EXIT).
LINES are the rows of the page and then one summary line; a refusal is one
`HELP FAIL` line at exit 2 ending `run: nova-work help`."
  (let ((words (%request-words request))
        (topic '()) (machine nil) (max *help-default-max*) (after 0))
    (unless (and words (string= (first words) "help"))
      (return-from help-answer (%help-fail "" "not a help request")))
    (loop with rest = (rest words)
          while rest
          do (let ((w (pop rest)))
               (cond
                 ((string= w "--machine") (setf machine t))
                 ((or (string= w "--max") (string= w "--after"))
                  (let ((n (if (string= w "--max")
                               (%help-count w (first rest) 1 *help-max-max*)
                               (%help-count w (first rest) 0 nil))))
                    (when (stringp n)
                      (return-from help-answer
                        (%help-fail (format nil "~{~A~^ ~}" (reverse topic)) n)))
                    (pop rest)
                    (if (string= w "--max") (setf max n) (setf after n))))
                 ((char= (char w 0) #\-)
                  (return-from help-answer
                    (%help-fail (format nil "~{~A~^ ~}" (reverse topic))
                                (format nil "unknown flag ~A" (%echo-safely w)))))
                 (t (push w topic)))))
    (let ((topic (format nil "~{~A~^ ~}" (reverse topic)))
          (schema (help-schema-hash)))
      (cond
        (machine
         (if (plusp (length topic))
             (%help-fail topic "--machine takes no family or verb; it lists every verb")
             (multiple-value-bind (page more next) (%help-page *verb-schema* after max)
               (values t
                       (append
                        (mapcar (lambda (v)
                                  (format nil "DISCOVERY VERB family=~A forms=~D sha256=~A verb=~A"
                                          (second v) (length (cddr v)) (%verb-forms-hash v) (first v)))
                                page)
                        (list (format nil "DISCOVERY OK protocol=1 families=~D verbs=~D rows=~D more=~D next=~A schema=~A"
                                      (length *help-families*) (length *verb-schema*)
                                      (length page) more next schema)))
                       0))))
        ((zerop (length topic))
         (%help-view "families" schema after max
                     (mapcar (lambda (f)
                               (format nil "HELP FAMILY family=~A verbs=~D covers=~A"
                                       (first f)
                                       (count (first f) *verb-schema* :key #'second :test #'string=)
                                       (second f)))
                             *help-families*)))
        ((assoc topic *help-families* :test #'string=)
         (%help-view "family" schema after max
                     (loop for v in *verb-schema*
                           when (string= (second v) topic)
                             collect (format nil "HELP VERB family=~A forms=~D verb=~A"
                                             (second v) (length (cddr v)) (first v)))))
        ((assoc topic *verb-schema* :test #'string=)
         (let* ((v (assoc topic *verb-schema* :test #'string=))
                (n (length (cddr v))))
           (%help-view "verb" schema after max
                       (loop for form in (cddr v) for i from 1
                             collect (format nil "HELP USAGE form=~D forms=~D usage=~A"
                                             i n (%help-one-line form))))))
        (t (%help-fail topic "no family or verb of that name"))))))

(defun %help-view (view schema after max rows)
  (multiple-value-bind (page more next) (%help-page rows after max)
    (values t
            (append page
                    (list (format nil "HELP OK view=~A rows=~D more=~D next=~A schema=~A"
                                  view (length page) more next schema)))
            0)))
