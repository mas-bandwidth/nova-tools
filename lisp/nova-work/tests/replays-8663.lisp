;;;; replays-8663.lisp --- five acceptance replays named in docs/SPEC-WORK.md.
;;;;
;;;; The names are the spec's own; each deftest sets up the state its paragraph
;;;; describes, drives the pure model the paragraph promises and asserts the
;;;; promised outcome. The live session, presence bus, journal and CLI wiring
;;;; those paragraphs sit on is out of this slice; where a name's paragraph
;;;; fixes the reading, the reading is stated in RESULT.md as
;;;; `read: <name>: ...`.
;;;;
;;;;   friend-falls-asleep-mid-lease           SPEC-WORK.md:4210
;;;;   delegated-to-a-sleeper-then-recovered   SPEC-WORK.md:4216
;;;;   lost-reply-then-reopen                  SPEC-WORK.md:5660
;;;;   release-one-allocation-spares-the-other SPEC-WORK.md:6141
;;;;   stale-allocation-id-refused-by-name     SPEC-WORK.md:6147

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; friend-falls-asleep-mid-lease                SPEC-WORK.md:4210
;;; ------------------------------------------------------------------

(deftest "friend-falls-asleep-mid-lease" "docs/SPEC-WORK.md:4210"
    "expected=holder-asleep-inside-300s;lease-untouched"
  (let ((lease (make-friend-lease :holder "stella" :node "acme/work/f1/t1")))
    ;; the holder stops beating; inside 300 s `stale` reads holder-asleep.
    (let ((finding (holder-asleep-finding lease 1000 1300)))
      (ok (and finding (search "finding=holder-asleep" finding))
          "a held lease past 300 s reads finding=holder-asleep: ~A" finding))
    ;; before the 300 s bound the holder is not yet asleep.
    (check-equal nil (holder-asleep-finding lease 1000 1299)
                 "inside 300 s the lease does not yet read holder-asleep")
    ;; reading a stale finding leaves the lease itself untouched.
    (holder-asleep-finding lease 1000 1300)
    (check-equal t (held-lease-p lease) "the lease is still held")
    (check-equal "stella" (friend-lease-holder lease) "the holder is unchanged")
    (check-equal :held (friend-lease-state lease) "the lease state is unchanged")))

;;; ------------------------------------------------------------------
;;; delegated-to-a-sleeper-then-recovered        SPEC-WORK.md:4216
;;; ------------------------------------------------------------------

(deftest "delegated-to-a-sleeper-then-recovered" "docs/SPEC-WORK.md:4216"
    "expected=offer-refused-exit-2;anyway-written-and-in-stale;reassigned-with-reading-cited;prior-lease-fenced;heartbeat-on-waking-refused;first-who-shows-reassignment"
  (let* ((sleeper (make-friend-presence :name "stella" :state :asleep))
         (lease (make-friend-lease :holder "stella" :node "acme/work/f1/t1"))
         (s (make-recovery-session :leases (list lease))))
    ;; an offer to an asleep friend is refused at exit 2 and writes nothing.
    (multiple-value-bind (ok line code s1)
        (offer-to-sleeper s sleeper :offer-id "off-1" :node "acme/work/f1/t1")
      (check-equal nil ok "an offer to an asleep friend is refused")
      (check-equal 2 code "the refusal is exit 2")
      (ok (search "asleep" line) "the refusal names the reading: ~A" line)
      (check-equal s s1 "the refused offer writes nothing"))
    ;; with --anyway the offer is written and appears in `stale`.
    (multiple-value-bind (ok line code s1)
        (offer-to-sleeper s sleeper :anyway t :offer-id "off-1"
                            :node "acme/work/f1/t1")
      (ok ok "an --anyway offer is written: ~A" line)
      (check-equal 0 code "the written offer is exit 0")
      (ok (some (lambda (e) (search "offer=off-1" e)) (stale-entries s1))
          "the written offer appears in stale: ~S" (stale-entries s1))
      ;; the reassignment cites the reading it stood on and fences the lease.
      (multiple-value-bind (ok2 line2 s2)
          (reassign-node s1 :node "acme/work/f1/t1" :from "stella" :to "glenn"
                            :reading "holder asleep" :rev 9)
        (ok ok2 "the reassignment is written")
        (ok (search "reassigned node=acme/work/f1/t1" line2)
            "the reassignment line names the node: ~A" line2)
        (ok (search "from=stella" line2) "the line names the prior holder: ~A" line2)
        (ok (search "to=glenn" line2) "the line names the new holder: ~A" line2)
        (ok (search "at=9" line2) "the line cites the revision: ~A" line2)
        (ok (search "reading=holder asleep" line2)
            "the line cites the reading: ~A" line2)
        (ok (lease-fenced-p
             (find "stella" (recovery-session-leases s2)
                   :key #'friend-lease-holder :test #'equal))
            "the prior lease is fenced")
        ;; the sleeper's heartbeat on waking is refused.
        (multiple-value-bind (hok hline hcode) (friend-heartbeat s2 "stella")
          (check-equal nil hok "the sleeper's heartbeat on waking is refused")
          (ok (plusp hcode) "the heartbeat refusal is a nonzero exit: ~A" hline))
        ;; its first who shows the reassignment.
        (let ((w (friend-who s2 "stella")))
          (ok (and w (search "reassigned" w))
              "the first who shows the reassignment: ~A" w)
          (ok (and w (search "to=glenn" w))
              "the who names the new holder: ~A" w))))))

;;; ------------------------------------------------------------------
;;; lost-reply-then-reopen                       SPEC-WORK.md:5660
;;; ------------------------------------------------------------------

(deftest "lost-reply-then-reopen" "docs/SPEC-WORK.md:5660"
    "expected=retry-returns-recorded-disposition;reopen-leaves-item-untouched;no-second-settle;fresh-id-validates-current-state"
  (let* ((k (fresh))
         (first (close-request :request "req-lost")))
    ;; the close is accepted and journaled; the reply to the client is lost.
    (multiple-value-bind (ok line code) (submit k first)
      (ok ok "the close is accepted and journaled: ~A" line)
      (check-equal 0 code "the close is exit 0")
      (check-equal :done (node-state (kernel-state k) "acme/work/f1/t1")
                   "the item is closed after the accepted mutation"))
    ;; a retry of the same request id returns its recorded disposition.
    (multiple-value-bind (ok line code env) (submit k first)
      (ok ok "the retry is answered OK")
      (check-equal 0 code "the retry is exit 0")
      (check-equal t (getf env :replayed) "the retry is a replay")
      (check-equal '() (getf env :events) "the retry applies nothing"))
    ;; the item is reopened.
    (multiple-value-bind (ok line) (submit k (reopen-request))
      (ok ok "the reopen is accepted: ~A" line)
      (check-equal :todo (node-state (kernel-state k) "acme/work/f1/t1")
                   "the item is reopened"))
    ;; even after the reopen, the same request id still returns its recorded
    ;; disposition and leaves the reopened item untouched.
    (multiple-value-bind (ok line code env) (submit k first)
      (ok ok "the stale retry is still answered OK")
      (check-equal 0 code "the stale retry is exit 0")
      (check-equal '() (getf env :events) "the replay applies no second settle")
      (check-equal :todo (node-state (kernel-state k) "acme/work/f1/t1")
                   "the reopened item is untouched by the replay"))
    ;; a fresh id validates current state and gives its normal effect.
    (multiple-value-bind (ok line) (submit k (close-request :request "req-fresh"))
      (ok (null ok) "a fresh close from todo is refused by validation: ~A" line))
    (multiple-value-bind (ok line)
        (submit k '(:verb :state-to-doing :node "acme/work/f1/t1" :by "rowan"
                    :reason "resume" :evidence ("ev-1") :request "req-start"
                    :stamp "2026-09-14T14:00:00Z" :clock :tool
                    :generation-owner "gen-4"))
      (ok ok "a fresh id validates current state and starts the item: ~A" line)
      (check-equal :doing (node-state (kernel-state k) "acme/work/f1/t1")
                   "the fresh-id invocation has its normal effect"))))

;;; ------------------------------------------------------------------
;;; release-one-allocation-spares-the-other     SPEC-WORK.md:6141
;;; ------------------------------------------------------------------

(deftest "release-one-allocation-spares-the-other" "docs/SPEC-WORK.md:6141"
    "expected=release-prints-freed;list-shows-one-row-for-alloc-b;slot-1-free-for-new-take;alloc-b-continues"
  (let* ((m (make-assign-machine :id "m-a1" :concurrent 2 :generation 7))
         (f0 (make-assign-fleet :machine m))
         (f1 (nth-value 3 (assign-fleet-take f0 :node "N1" :slots 1 :assign-machine-generation 7
                                      :holder "n1" :allocation-id "alloc-a")))
         (f2 (nth-value 3 (assign-fleet-take f1 :node "N2" :slots 1 :assign-machine-generation 7
                                      :holder "n2" :allocation-id "alloc-b"))))
    ;; machine m-a1 holds alloc-a (slot 1, N1) and alloc-b (slot 2, N2).
    (check-equal 1 (allocation-slot (find "alloc-a" (assign-fleet-allocations f2)
                                          :key #'allocation-id :test #'equal))
                 "alloc-a holds slot 1")
    (check-equal 2 (allocation-slot (find "alloc-b" (assign-fleet-allocations f2)
                                          :key #'allocation-id :test #'equal))
                 "alloc-b holds slot 2")
    ;; release alloc-a prints its exact ALLOC RELEASE line with freed=true.
    (multiple-value-bind (ok line code f3) (assign-fleet-release f2 :allocation "alloc-a" :generation 7)
      (ok ok "the release is accepted: ~A" line)
      (check-equal 0 code "the release is exit 0")
      (ok (search "ALLOC RELEASE OK" line) "the release prints its OK line: ~A" line)
      (ok (search "allocation=alloc-a" line) "the line names alloc-a: ~A" line)
      (ok (search "machine=m-a1" line) "the line names the machine: ~A" line)
      (ok (search "slot=1" line) "the line names the freed slot: ~A" line)
      (ok (search "freed=true" line) "the line prints freed=true: ~A" line)
      ;; list --machine m-a1 shows exactly one ALLOC ROW, for alloc-b slot 2.
      (let ((rows (assign-fleet-list f3)))
        (check-equal 1 (length rows) "exactly one allocation row remains")
        (ok (search "allocation=alloc-b" (first rows))
            "the surviving row is alloc-b: ~A" (first rows))
        (ok (search "slot=2" (first rows))
            "alloc-b still holds slot 2: ~A" (first rows)))
      ;; the freed slot 1 admits a new take while alloc-b continues.
      (multiple-value-bind (ok2 line2 code2 f4)
          (assign-fleet-take f3 :node "N3" :slots 1 :assign-machine-generation 7
                         :holder "n3" :allocation-id "alloc-d")
        (check-equal 0 code2 "the new take is exit 0")
        (ok ok2 "the freed slot admits a new take: ~A" line2)
        (check-equal 1 (allocation-slot (find "alloc-d" (assign-fleet-allocations f4)
                                              :key #'allocation-id :test #'equal))
                     "the new take gets the freed slot 1")
        (ok (allocation-active-p (find "alloc-b" (assign-fleet-allocations f4)
                                       :key #'allocation-id :test #'equal))
            "alloc-b continues untouched")
        (check-equal 2 (length (assign-fleet-list f4))
                     "the list shows both live allocations")))))

;;; ------------------------------------------------------------------
;;; stale-allocation-id-refused-by-name         SPEC-WORK.md:6147
;;; ------------------------------------------------------------------

(deftest "stale-allocation-id-refused-by-name" "docs/SPEC-WORK.md:6147"
    "expected=heartbeat-of-released-refused-by-name;release-of-released-refused-by-name;stale-assign-machine-generation-take-refused-capacity"
  (let* ((m (make-assign-machine :id "m-a1" :concurrent 2 :generation 7))
         (f0 (make-assign-fleet :machine m))
         (f1 (nth-value 3 (assign-fleet-take f0 :node "N1" :slots 1 :assign-machine-generation 7
                                      :holder "n1" :allocation-id "alloc-a")))
         (f2 (nth-value 3 (assign-fleet-release f1 :allocation "alloc-a" :generation 7))))
    ;; a heartbeat using the released allocation id is refused by name at exit 1.
    (multiple-value-bind (ok line code) (assign-fleet-heartbeat f2 :allocation "alloc-a" :generation 7)
      (check-equal nil ok "a heartbeat for the released allocation is refused")
      (check-equal 1 code "the refusal is exit 1")
      (ok (search "ALLOC FAIL machine=m-a1 allocation=alloc-a: stale token" line)
          "the refusal names the stale allocation: ~A" line))
    ;; a release using the released allocation id is refused by name at exit 1.
    (multiple-value-bind (ok line code) (assign-fleet-release f2 :allocation "alloc-a" :generation 7)
      (check-equal nil ok "a second release is refused")
      (check-equal 1 code "the refusal is exit 1")
      (ok (search "allocation=alloc-a" line) "the refusal names alloc-a: ~A" line)
      (ok (search "stale token" line) "the refusal is stale token: ~A" line))
    ;; a take using the released allocation's generation against a changed
    ;; machine configuration is refused by the stale machine generation check.
    (let ((f3 (make-assign-fleet
               :machine (make-assign-machine :id "m-a1" :concurrent 2 :generation 8))))
      (multiple-value-bind (ok line code)
          (assign-fleet-take f3 :node "N3" :slots 1 :assign-machine-generation 7
                         :holder "n3" :allocation-id "alloc-e")
        (check-equal nil ok "a take with a stale machine generation is refused")
        (check-equal 1 code "the refusal is exit 1")
        (ok (search "ALLOC FAIL machine=m-a1" line) "the refusal names the machine: ~A" line)
        (ok (search "slots=1" line) "the refusal names the requested slots: ~A" line)
         (ok (search "holder=n3" line) "the refusal names the holder: ~A" line)
         (ok (search "capacity" line) "the refusal is the capacity one: ~A" line)))))

;;; ------------------------------------------------------------------
;;; TestE06F04ComparePriorCheckpointToCurrent    SPEC-WORK.md:7085
;;;    criterion E06-F04-02 in docs/roadmaps/nova-work.sexp:
;;;    "Compare prior checkpoint to current state and report gaps".
;;;    The recovery row fixes the reading: "compare an isolated old
;;;    restore against current state; a missing tail or an unavailable
;;;    remote backup reported as a recovery gap". `savepoint compare`
;;;    puts the prior checkpoint's revision and the current state's
;;;    revision side by side, and reports as gaps every revision the
;;;    current state holds beyond the prior checkpoint.
;;; ------------------------------------------------------------------

(deftest "TestE06F04ComparePriorCheckpointToCurrent" "docs/SPEC-WORK.md:7085"
    "expected=prior-and-current-revisions-side-by-side;gap-revisions-named-one-each;unshared-not-shared;gap-count-not-a-fold"
  (let* ((prior (make-savepoint
                 :id "sp-prior" :schema "work-savepoint-v1" :local-revision 812
                 :journal-id "abc" :replay-cut '(:sequence 420 :sha256 "h1")
                 :boundary '(:sequence 390 :sha256 "h2") :manifest "sha-1"
                 :image "state" :local-replies "replies" :age 3600
                 :failed-backup nil))
         (current (list :kind :session
                        :revision 820
                        :checkpoint 750
                        :records (list (list :seq 1 :revision 700 :events '(1))
                                       (list :seq 2 :revision 812 :events '(2))
                                       (list :seq 3 :revision 819 :events '(3))
                                       (list :seq 4 :revision 820 :events '(4)))))
         (cmp (savepoint-compare prior :against current)))
    ;; the prior checkpoint's revision and the current state's revision stand
    ;; side by side; neither is folded into the other.
    (check-equal 812 (getf cmp :local-revision)
                 "the prior checkpoint's revision is named")
    (check-equal 820 (getf cmp :against-revision)
                 "the current state's revision is named beside it")
    (check-equal 750 (getf cmp :shared-checkpoint)
                 "the shared checkpoint stays a different field")
    ;; the gaps are reported, one by one: every revision the current state holds
    ;; beyond the prior checkpoint is named, never rounded into a count alone.
    (check-equal '(819 820) (getf cmp :missing)
                 "the work the current state holds beyond the prior checkpoint is reported as gaps")
    ;; the work above the shared checkpoint still inside the prior checkpoint is
    ;; reported as unshared, and the local savepoint is never the shared backup.
    (check-equal '(812) (getf cmp :unshared)
                 "the unshared work above the shared checkpoint is reported")
    (check-equal nil (getf cmp :shared)
                 "the local savepoint is not reported as the shared checkpoint")
    ;; the comparison line names both revisions and reports each gap count.
    (ok (search "rev=812" (getf cmp :line))
        "the compare line names the prior revision: ~A" (getf cmp :line))
    (ok (search "against=820" (getf cmp :line))
        "the compare line names the current revision: ~A" (getf cmp :line))
    (ok (search "checkpoint=750" (getf cmp :line))
        "the compare line names the shared checkpoint separately: ~A" (getf cmp :line))
    (ok (search "missing=2" (getf cmp :line))
        "the compare line reports the gap count: ~A" (getf cmp :line))
    (ok (search "unshared=1" (getf cmp :line))
        "the compare line reports the unshared count: ~A" (getf cmp :line))))

;;; ------------------------------------------------------------------
;;; TestE09F04ArchiveSourceIdentityProvenanceAnd   SPEC-WORK.md:7595-7610
;;;    acceptance criterion E09-F04-02 in docs/roadmaps/nova-work.sexp:
;;;    "Archive source identity, provenance and content before removal;
;;;    append the actual deletion outcome receipt after the attempt".
;;;    SPEC-WORK.md:7595-7597 orders the archive before any removal and lists
;;;    what must be retained -- the source's identity, authorship, body,
;;;    discussion, labels, state, relationships and attachments, provenance
;;;    kept separate from planning; :7609-7610 requires the removed issue to
;;;    keep its external identity plus a deletion receipt recording the actual
;;;    outcome, so later intake cannot recreate the work. The absorb operation
;;;    is disabled in the v1 pilot (:7614); the archive model (archive-capture)
;;;    retains only a source label, an author class and its gaps. This replay
;;;    holds the pre-deletion archive a source builds and asserts the two
;;;    behaviours: it is RED until the archive retains identity, provenance and
;;;    content and the removal attempt appends its deletion receipt.
;;; ------------------------------------------------------------------

(deftest "TestE09F04ArchiveSourceIdentityProvenanceAnd" "docs/SPEC-WORK.md:7595-7610"
    "expected=archive-retains-source-identity-provenance-content-before-removal;deletion-outcome-receipt-appended-after-the-attempt"
  (let ((capture (make-archive-capture
                  :source-issue "https://github.com/acme/widget/issues/7"
                  :author :known :gaps '())))
    (declare (ignore capture))
    (labels ((retains-p (name)
               "True when the archive model exposes an accessor for NAME."
               (let ((s (find-symbol name "NOVA-WORK")))
                 (and s (fboundp s)))))
      (ok (and (retains-p "ARCHIVE-CAPTURE-IDENTITY")
               (retains-p "ARCHIVE-CAPTURE-PROVENANCE")
               (retains-p "ARCHIVE-CAPTURE-CONTENT"))
          "expected the archive to retain the source's identity, provenance and content before removal (SPEC-WORK.md:7595-7597); the archive model retains only a source label, an author class and its gaps")
      (ok (retains-p "ARCHIVE-CAPTURE-DELETION-RECEIPT")
          "expected the removal attempt to append the actual deletion outcome receipt (SPEC-WORK.md:7609-7610); the archive model records no deletion outcome"))))
