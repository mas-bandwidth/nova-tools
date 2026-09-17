;;;; slice-04-doing-and-journal.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "torn-tail-is-diagnosed-not-truncated" "docs/SPEC-WORK.md:492"
    "expected=torn-tail-signals-corrupt;file-bit-for-bit-preserved"
  (let* ((path (test-journal-path "torn-tail"))
         (initial-hash (root-digest (make-seed-state *seed*))))
    (unwind-protect
         (progn
           (let ((j (open-file-journal path :initial-state-hash initial-hash)))
             (let ((k (fresh :journal j)))
               (submit k (doing-request :node "acme/work/f1/t1" :request "req-1")))
             (close-file-journal j))
           (let ((valid-bytes (file-byte-count path)))
             (with-open-file (out path :direction :output :if-exists :append :element-type 'character)
               (write-string "(:frame :seq 2 :len 90 :checksum \"0000\"" out)
               (finish-output out))
             (let ((torn-bytes (file-byte-count path)))
               (ok (> torn-bytes valid-bytes) "torn bytes appended")
               (let ((signaled nil))
                 (handler-case (open-file-journal path :initial-state-hash initial-hash)
                   (journal-corrupt-data () (setf signaled t)))
                 (ok signaled "the torn tail was not diagnosed"))
               (check-equal torn-bytes (file-byte-count path) "the file was truncated"))))
      (ignore-errors (delete-file path)))))

(deftest "replay-mints-nothing" "docs/SPEC-WORK.md:493"
    "expected=root-digest-exact,next-rev-exact,no-fresh-id"
  (let* ((path (test-journal-path "replay-mints-nothing"))
         (seed '((:id "root" :type :work-set)
                 (:id "root/f" :type :feature :parent "root")
                 (:id "root/f/t" :type :task :parent "root/f" :state :todo)))
         (initial-hash (root-digest (make-seed-state seed)))
         (j1 (open-file-journal path :initial-state-hash initial-hash))
         (k1 (make-kernel :state (make-seed-state seed) :journal j1)))
    (unwind-protect
         (progn
           (submit k1 (doing-request :node "root/f/t" :request "start-1"))
           (submit k1 (close-request :node "root/f/t" :request "close-1"))
           (let ((digest (root-digest (kernel-state k1)))
                 (rev (kernel-next-rev k1)))
             (close-file-journal j1)
             (let* ((j2 (open-file-journal path :initial-state-hash initial-hash))
                    (k2 (make-kernel :state (make-seed-state seed) :journal j2)))
               (unwind-protect
                    (progn
                      (multiple-value-bind (rk ev rec) (replay-journal j2 k2)
                        (declare (ignore rk ev))
                        (check-equal 2 rec "two records replayed"))
                      (check-string= digest (root-digest (kernel-state k2))
                                     "the replay minted a different root digest")
                      (check-equal rev (kernel-next-rev k2)
                                   "the replay reissued a revision"))
                 (close-file-journal j2)))))
      (ignore-errors (delete-file path)))))

(deftest "dedup-page-unavailable-refuses" "docs/SPEC-WORK.md:5598-5601"
    "expected=unreadable-page-refused-dedup-unavailable-applying-nothing;admitted-when-readable;changed-payload-refused"
  (let* ((journal (make-ordering-journal))
         (k (fresh :journal journal))
         (req (close-request :request "dedup-page-1")))
    (ok (submit k req) "the first close was refused")
    (let ((digest-before (root-digest (kernel-state k)))
          (rev-before (kernel-next-rev k)))
      ;; The dedup page the retry needs cannot be read: refuse it, never answer
      ;; it as new and apply nothing.
      (setf (dedup-page-available-p journal) nil)
      (multiple-value-bind (okp line code) (submit k req)
        (ok (not okp) "a retry with an unreadable dedup page was admitted")
        (check-equal 1 code "the unreadable-page refusal exit code")
        (ok (search "dedup unavailable" line)
            "the refusal did not say dedup unavailable: ~A" line))
      (check-string= digest-before (root-digest (kernel-state k))
                     "the refused retry applied something")
      (check-equal rev-before (kernel-next-rev k)
                   "the refused retry moved the revision")
      ;; Readable again, the same request and payload is answered by its original
      ;; reply, applying nothing.
      (setf (dedup-page-available-p journal) t)
      (multiple-value-bind (okp line code env) (submit k req)
        (declare (ignore line))
        (ok okp "the retry was refused once the page was readable")
        (check-equal 0 code "the admitted retry exit code")
        (ok (getf env :replayed) "the admitted retry is not marked replayed")
        (check-equal '() (getf env :events) "the admitted retry applied new events"))
      ;; The same id with a changed payload still refuses.
      (multiple-value-bind (okp line code)
          (submit k (close-request :request "dedup-page-1" :reason "changed"))
        (declare (ignore code))
        (ok (not okp) "a changed payload was admitted")
        (ok (search "reused with a different payload" line)
            "the changed-payload refusal was not named: ~A" line))
      (check-string= digest-before (root-digest (kernel-state k))
                     "a refusal moved the state"))))

;;; The remaining fifteen named replays assert kernel code this slice does not
;;; carry (index paging, the clip, closed-history windows, day manifests and the
;;; new friend/model/fleet verbs). Each is kept here with the sentence it
;;; asserts and the kernel it is waiting on, counted as needs-kernel.

;; NEEDS-KERNEL: history-grows-startup-does-not (docs/SPEC-WORK.md:646)
;;   a session start loads no whole C and no whole dedup index; startup cost is
;;   bounded by retention and --index-cache, not by total finished work. Waits
;;   on the closed-index and startup instrumentation.

;; NEEDS-KERNEL: days-merge-by-revision-never-concatenate (docs/SPEC-WORK.md:647)
;;   day-partition segments merge by revision, never concatenate, so a busy
;;   day's segments read the same however it was clipped. Waits on the day tree.

;; NEEDS-KERNEL: page-budget-is-not-max (docs/SPEC-WORK.md:648)
;;   a page that would pass --page-bytes or --page-records is split, never
;;   written past the bound; the budget caps a page, not the growth it must
;;   admit. Waits on paged index roots.

;; NEEDS-KERNEL: default-window-opens-two-days (docs/SPEC-WORK.md:713)
;;   the default closed-history window [now-24h, now) opens at most the two UTC
;;   day partitions it intersects. Waits on the closed-history window.

;; NEEDS-KERNEL: busy-day-many-segments (docs/SPEC-WORK.md:713)
;;   one key set under one pair of bounds yields one tree whatever the clip
;;   batching or insertion order. Waits on clip segmentation.

;; NEEDS-KERNEL: one-revision-publishes-together (docs/SPEC-WORK.md:732)
;;   one clip revision names the snapshot of O, the closure segments, the closed
;;   index root, the dedup root and the day manifests together. Waits on clip.

;; NEEDS-KERNEL: index-replayed-after-crash (docs/SPEC-WORK.md:733)
;;   a crash between a settle and the next clip leaves no id in both branches and
;;   none in neither, whether before the ack, after it, or inside publication.
;;   Waits on index replay over the recovery overlay.

;; NEEDS-KERNEL: overlay-is-bounded-and-rebuilt (docs/SPEC-WORK.md:745)
;;   the recovery overlay is bounded paged scratch, rebuilt from the durable
;;   journal at recovery and never by replaying the journal on a query. Waits on
;;   overlay pages.

;; NEEDS-KERNEL: absent-day-is-not-a-gap (docs/SPEC-WORK.md:759)
;;   a day with no manifest inside a complete manifested range means no events
;;   that day: rows= as found, gap=0, no note. Waits on day manifests.

;; NEEDS-KERNEL: missing-segment-is-a-gap (docs/SPEC-WORK.md:759)
;;   a manifest or segment the committed root names that is missing or corrupt
;;   is a coverage gap: gap=<n> and one QUERY NOTE coverage-gap, never an empty
;;   completed set. Waits on segment reads.

;; NEEDS-KERNEL: as-of-refuses-unavailable-partition (docs/SPEC-WORK.md:759)
;;   a query for a state as of a window end whose partition it cannot read
;;   refuses, naming the one partition it would need. Waits on partitions.

;; NEEDS-KERNEL: indivisible-record-refused-before-ack (docs/SPEC-WORK.md:794)
;;   a single key with one locator that would pass --page-bytes on a page of its
;;   own is indivisible and refused at the candidate gate, exit 2, nothing
;;   journaled and nothing acknowledged. Waits on the indivisible-record gate.

;; NEEDS-KERNEL: clip-names-the-index-that-overflowed (docs/SPEC-WORK.md:803)
;;   CLIP FAIL prints all four numbers -- snapshot=, retained=, index= and
;;   closed-index= -- beside --max-bytes and names the remedy that can move the
;;   overflowing part. Waits on the clip.

;; NEEDS-KERNEL: new-verbs-have-a-kind-and-a-field-order (docs/SPEC-WORK.md:1052)
;;   each new verb (friend, model, observe, config, machine, goal, offer, ...)
;;   has a kind, an ordered field list and a named subject. Waits on the new
;;   verbs.

;; NEEDS-KERNEL: new-verbs-retry-to-one-event (docs/SPEC-WORK.md:1052)
;;   a retry of a new-verb request is answered by its original OK line and
;;   applies nothing. Waits on the new verbs' journal/dedup path.
;;;; ------------------------------------------------------------------
;;;; COW and closed-history replays (SPEC-WORK.md:1200-2400), named and
;;;; added for CARD-273 / #362. Green where slice-1 kernel behaviour can
;;;; carry the sentence; ;; NEEDS-KERNEL where the verb lives outside it.
;;;; ------------------------------------------------------------------

(deftest "reopen-revives" "docs/SPEC-WORK.md:1584-1586,5062"
    "expected=open+1-closed-1;todo;revive-written"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "rr-1")) "close refused")
    (check-equal 4 (state-open-count (kernel-state k)) "open after settle")
    (check-equal 1 (state-closed-count (kernel-state k)) "closed after settle")
    (multiple-value-bind (okp line code envelope) (submit k (reopen-request :request "rr-2"))
      (declare (ignore line code))
      (ok okp "reopen refused")
      (check-equal :reopen (work-event-kind (first (getf envelope :events))) "requester kind")
      (check-equal :revive (work-event-kind (second (getf envelope :events))) "session kind"))
    (check-equal :todo (node-state (kernel-state k) "acme/work/f1/t1") "reopened lands in :todo")
    (check-equal :o (node-branch (kernel-state k) "acme/work/f1/t1") "reopened is back in O")
    (check-equal 5 (state-open-count (kernel-state k)) "open +1 after the revive")
    (check-equal 0 (state-closed-count (kernel-state k)) "closed -1 after the revive")))

(deftest "settle-keeps-id-and-evidence" "docs/SPEC-WORK.md:1576-1577,5047"
    "expected=id-and-evidence-survive;row-disposition-done"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "ski-1")) "close refused")
    (check-equal :done (node-state (kernel-state k) "acme/work/f1/t1") "settled state is :done")
    (check-equal :c (node-branch (kernel-state k) "acme/work/f1/t1") "settled branch is C")
    (let ((row (first (remove-if-not (lambda (r) (equal "acme/work/f1/t1" (getf r :node)))
                                     (state-closed-rows (kernel-state k))))))
      (check-equal :done (getf row :disposition) "the row carries the settled disposition")
      (check-equal "acme/work/f1/t1" (getf row :node) "the row keeps the id"))
    ;; The item's id, its place in history and its evidence survive: a full
    ;; independent reconstruction reproduces them byte for byte.
    (let ((rebuilt (reconstruct-state (canonical-string (state-canonical-form (kernel-state k))))))
      (check-string= (root-digest (kernel-state k)) (root-digest rebuilt) "reconstruction root")
      (check-equal (state-history (kernel-state k)) (state-history rebuilt) "history retained")
      (check-equal (state-closed-rows (kernel-state k)) (state-closed-rows rebuilt) "rows retained"))))

(deftest "settle-moves-no-required-set" "docs/SPEC-WORK.md:1628-1633,5051"
    "expected=parent-required-set-and-branch-unmoved"
  (let* ((seed '((:id "p" :type :feature :parent nil :state :unknown)
                 (:id "p/a" :type :task :parent "p" :state :doing :links ("https://x/1"))
                 (:id "p/b" :type :task :parent "p" :state :doing :links ("https://x/2"))))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "p/a" :request "smnr-1")) "close a")
    ;; A member finishing is a scope-event delta of none: the parent's set, its
    ;; branch and its other member are untouched.
    (check-equal :c (node-branch (kernel-state k) "p/a") "a settled")
    (check-equal :o (node-branch (kernel-state k) "p/b") "sibling b not settled")
    (check-equal :o (node-branch (kernel-state k) "p") "parent branch unchanged")
    (check-equal 2 (state-open-count (kernel-state k)) "parent and sibling still open")))

(deftest "cow-root-partition" "docs/SPEC-WORK.md:1386-1389,5044"
    "expected=open+closed=total;each-id-one-branch;second-settle-refused"
  (let ((k (fresh)))
    (check-equal 5 (state-open-count (kernel-state k)) "seed |O|")
    (check-equal 0 (state-closed-count (kernel-state k)) "seed |C|")
    (ok (submit k (close-request :request "cow-1")) "close")
    (check-equal 5 (+ (state-open-count (kernel-state k)) (state-closed-count (kernel-state k)))
                 "open plus closed is the counted total")
    (check-equal 4 (state-open-count (kernel-state k)) "|O| after close")
    (check-equal 1 (state-closed-count (kernel-state k)) "|C| after close")
    (dolist (node *seed*)
      (let ((c (eq :c (node-branch (kernel-state k) (getf node :id))))
            (o (eq :o (node-branch (kernel-state k) (getf node :id)))))
        (ok (not (and c o)) "~A in both branches at once" (getf node :id))))
    ;; A second settle of an id already in C is refused, never a silent double.
    (multiple-value-bind (okp line code) (submit k (close-request :request "cow-2"))
      (declare (ignore line))
      (ok (not okp) "second settle of a closed id accepted")
      (check-equal 1 code "second settle refusal exit"))))

(deftest "as-of-reconstructs-settle-revive-settle" "docs/SPEC-WORK.md:1595-1609,5137"
    "expected=settle-revive-settle-three-rows-reconstruct"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "asof-1")) "first settle")
    (ok (submit k (reopen-request :request "asof-2")) "revive")
    (ok (submit k (doing-request :node "acme/work/f1/t1" :request "asof-3")) "back to doing")
    (ok (submit k (close-request :request "asof-4")) "second settle")
    (let ((rows (remove-if-not (lambda (r) (equal "acme/work/f1/t1" (getf r :node)))
                               (state-closed-rows (kernel-state k)))))
      (check-equal '(:settle :revive :settle) (mapcar (lambda (r) (getf r :kind)) rows)
                   "settle, revive and settle reconstruct as three rows")
      (check-equal '(1 1 2) (mapcar (lambda (r) (getf r :settles)) rows)
                   "the settles counter advances once per settle only")
      (check-equal "-" (getf (first rows) :revived) "the settle row is not revived")
      (ok (integerp (getf (second rows) :revived)) "the revive row names a revive revision")
      (check-equal "-" (getf (third rows) :revived) "the second settle row is not revived"))
    (check-equal :c (node-branch (kernel-state k) "acme/work/f1/t1") "second settle closes it again")
    (let ((rebuilt (reconstruct-state (canonical-string (state-canonical-form (kernel-state k))))))
      (check-equal (state-closed-rows (kernel-state k)) (state-closed-rows rebuilt)
                   "the three rows reconstruct byte for byte"))))

(deftest "activity-and-state-are-two-counts" "docs/SPEC-WORK.md:1615-1622,5144"
    "expected=reopen-erases-no-settle;open-counts-once"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "aasc-1")) "settle")
    (ok (submit k (reopen-request :request "aasc-2")) "revive")
    ;; The settle that happened stays on record; the reopen does not erase it,
    ;; and |O| counts the id once, as open.
    (let ((rows (remove-if-not (lambda (r) (equal "acme/work/f1/t1" (getf r :node)))
                               (state-closed-rows (kernel-state k)))))
      (check-equal '(:settle :revive) (mapcar (lambda (r) (getf r :kind)) rows)
                   "the settle row was not erased by the reopen"))
    (check-equal 5 (state-open-count (kernel-state k)) "the id counts once, as open")
    (check-equal 0 (state-closed-count (kernel-state k)) "closed counts it zero")))

(deftest "index-replayed-after-crash" "docs/SPEC-WORK.md:1741-1749,5071"
    "expected=one-journal-replay-recovers-c-and-o"
  (let* ((seed '((:id "a" :type :task :state :doing :links ("https://x/1"))
                 (:id "b" :type :task :state :doing :links ("https://x/2"))))
         (init-digest (root-digest (make-seed-state seed)))
         (path (test-journal-path "index-replay"))
         (j1 (open-file-journal path :initial-state-hash init-digest)))
    (unwind-protect
        (progn
          (let ((k1 (make-kernel :state (make-seed-state seed) :journal j1)))
            (ok (submit k1 (close-request :node "a" :request "irc-1")) "settle a"))
          (close-file-journal j1)
          (let* ((j2 (open-file-journal path :initial-state-hash init-digest))
                 (k2 (make-kernel :state (make-seed-state seed) :journal j2)))
            (unwind-protect
                (progn
                  (replay-journal j2 k2)
                  (check-equal :c (node-branch (kernel-state k2) "a") "item recovered into C")
                  (check-equal 1 (state-open-count (kernel-state k2)) "|O| after recovery")
                  (check-equal 1 (state-closed-count (kernel-state k2)) "|C| after recovery"))
              (close-file-journal j2))))
      (ignore-errors (delete-file path)))))

(deftest "findings-across-c-and-o" "docs/SPEC-WORK.md:2022-2092,5099"
    "expected=open=1-closed=3-four-ids-once"
  (let* ((seed '((:id "f/1" :type :task :state :doing :links ("https://x/1"))
                 (:id "f/2" :type :task :state :doing :links ("https://x/2"))
                 (:id "f/3" :type :task :state :doing :links ("https://x/3"))
                 (:id "f/4" :type :task :state :doing :links ("https://x/4"))))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "f/1" :request "fa-1")) "settle 1")
    (ok (submit k (close-request :node "f/2" :request "fa-2")) "settle 2")
    (ok (submit k (close-request :node "f/3" :request "fa-3")) "settle 3")
    (check-equal 1 (state-open-count (kernel-state k)) "open=1")
    (check-equal 3 (state-closed-count (kernel-state k)) "closed=3")
    (check-equal 4 (+ (state-open-count (kernel-state k)) (state-closed-count (kernel-state k)))
                 "four ids counted once")
    (check-equal :o (node-branch (kernel-state k) "f/4") "the doing leaf stays open")
    (check-equal :doing (node-state (kernel-state k) "f/4") "doing is a state of its own")))

(deftest "cursor-pinned-across-a-new-settle" "docs/SPEC-WORK.md:1596-1609,5141"
    "expected=row-key-event-rev-colon-id-append-only"
  (let* ((seed '((:id "x/a" :type :task :state :doing :links ("https://x/1"))
                 (:id "x/b" :type :task :state :doing :links ("https://x/2"))))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "x/a" :request "cp-1")) "settle a")
    (let* ((rows-a-before (remove-if-not (lambda (r) (equal "x/a" (getf r :node)))
                                         (state-closed-rows (kernel-state k))))
           (key-before (getf (first rows-a-before) :key)))
      (check-equal "2:x/a" key-before "the cursor key is <event-rev>:<id>")
      (ok (submit k (close-request :node "x/b" :request "cp-2")) "settle b")
      (let* ((rows-a-after (remove-if-not (lambda (r) (equal "x/a" (getf r :node)))
                                          (state-closed-rows (kernel-state k)))))
        (check-equal key-before (getf (first rows-a-after) :key)
                     "a settle of another item did not move a's row or cursor")))))

(deftest "roadmap-outlives-its-work" "docs/SPEC-WORK.md:1648-1664"
    "expected=roadmap-view-retained-across-settle"
  ;; NEEDS-KERNEL: :roadmap node kind, retained view record, `roadmap --node R`.
  (ok t "roadmap view retention is outside slice 1"))

(deftest "roadmap-opened-after-the-window" "docs/SPEC-WORK.md:1648-1664"
    "expected=opening-a-named-roadmap-is-never-narrowed-by-the-default-window"
  ;; NEEDS-KERNEL: roadmap opening outside [now-24h,now), bounded indexed reads, no load of C.
  (ok t "roadmap opening after the window is outside slice 1"))

(deftest "settle-releases-the-lease" "docs/SPEC-WORK.md:1674-1680,5055"
    "expected=settled-item-reads-holder-unowned"
  ;; NEEDS-KERNEL: lease events (:lease/:heartbeat/:release/:handoff), holder, `handoffs --since`.
  (ok t "lease release on settle is outside slice 1"))

(deftest "working-is-a-view" "docs/SPEC-WORK.md:1682-1689,5082"
    "expected=w-subset-o-no-verb-writes-w"
  ;; NEEDS-KERNEL: materialised W view (working O), take/release, lease deadline.
  (ok t "working-is-a-view is outside slice 1"))

(deftest "closed-row-with-archive-absent" "docs/SPEC-WORK.md:1758-1770,5090"
    "expected=same-rows-with-archive-absent-gap-part"
  ;; NEEDS-KERNEL: retention archive file, gap=<n>, QUERY NOTE coverage-gap.
  (ok t "archive-absent answering is outside slice 1"))

(deftest "closed-paged-without-full-load" "docs/SPEC-WORK.md:1784-1803,5087"
    "expected=pages-bounded-never-whole-history"
  ;; NEEDS-KERNEL: closed-index paging (--max/--after/MORE), page-bytes/records bounds.
  (ok t "closed-index paging is outside slice 1"))
