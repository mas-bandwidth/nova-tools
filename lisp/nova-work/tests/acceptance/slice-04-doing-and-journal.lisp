;;;; slice-04-doing-and-journal.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "torn-tail-is-diagnosed-not-truncated" "docs/SPEC-WORK.md:492"
    "expected=torn-tail-signals-corrupt;file-bit-for-bit-preserved;torn-tail-and-corrupt-record-and-journal-mismatch-each-by-its-kind;none-rounded"
  (let* ((path (test-journal-path "torn-tail"))
         (initial-hash (root-digest (make-seed-state *seed*))))
    (unwind-protect
         (progn
           (let ((j (open-file-journal path :initial-state-hash initial-hash)))
             (let ((k (fresh :journal j)))
               (submit k (doing-request :node "acme/work/f1/t1" :request "req-1")))
             (close-file-journal j))
           (let* ((valid-bytes (file-byte-count path))
                  (valid-sha (file-sha256-hex path)))
             (with-open-file (out path :direction :output :if-exists :append :element-type 'character)
               (write-string "(:frame :seq 2 :len 90 :checksum \"0000\"" out)
               (finish-output out))
             (let* ((torn-bytes (file-byte-count path))
                    (torn-sha (file-sha256-hex path)))
               (ok (> torn-bytes valid-bytes) "torn bytes appended")
               (let ((reason nil))
                 (handler-case (open-file-journal path :initial-state-hash initial-hash)
                   (journal-corrupt-data (c) (setf reason (journal-corrupt-data-reason c))))
                 (ok reason "the torn tail was not diagnosed"))
               (check-equal torn-bytes (file-byte-count path)
                            "the file was truncated")
               (check-string= torn-sha (file-sha256-hex path)
                              "the file is bit for bit as it was"))))
      (ignore-errors (delete-file path))))
  ;; savepoint verify tells the three gaps apart and rounds none of them to
  ;; another: a torn tail is an interrupted append, a flipped bit is a corrupt
  ;; record and a wrong journal id is a journal mismatch
  ;; (docs/SPEC-WORK.md:6124-6130, :6454-6459).
  (let* ((records (list (list :seq 1 :request "r1" :reply '("OK r1") :payload-sha256 "p1" :events '(1))))
         (journal (make-journal-chain
                   :id "journal-abc"
                   :segments (list (make-journal-segment :path "journal.1"
                                                         :header '() :records '()))))
         (sp (savepoint-store-verified
              (savepoint-write (make-savepoint-store) "sp-1" 1 1 records :journal journal)))
         (image (savepoint-image-events 1 records))
         (replies (savepoint-retained-replies records))
         (torn (nth-value 1
                          (savepoint-verify (make-savepoint-load
                                             :savepoint sp :journal journal
                                             :image image :replies replies
                                             :records records :gap :torn-tail))))
         (corrupt (nth-value 1
                             (savepoint-verify (make-savepoint-load
                                                :savepoint sp :journal journal
                                                :image (list :events '(99))
                                                :replies replies :records records))))
         (mismatch (nth-value 1
                              (savepoint-verify
                               (make-savepoint-load :savepoint sp :image image
                                                    :replies replies :records records
                                                    :journal (make-journal-chain :id "other")))))
         (verified (savepoint-verify (make-savepoint-load :savepoint sp :journal journal
                                                          :image image :replies replies
                                                          :records records))))
    (check-equal :verified verified "a whole savepoint verifies")
    (ok (search "recovery-gap kind=torn-tail" torn) "a torn tail is named: ~A" torn)
    (ok (search "recovery-gap kind=corrupt-record" corrupt)
        "a flipped bit is a corrupt record: ~A" corrupt)
    (ok (search "journal mismatch" mismatch) "a wrong header is a journal mismatch: ~A" mismatch)
    (ok (not (equal torn corrupt)) "a torn tail is not rounded to a corrupt record")
    (ok (not (equal corrupt mismatch)) "a corrupt record is not rounded to a journal mismatch")))

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

;; history-grows-startup-does-not now lives in tests/acceptance.lisp over the
;; closed-history model of src/replays-closed-history.lisp (nova-tools #362).
;; days-merge-by-revision-never-concatenate now lives in tests/acceptance.lisp
;; over the closed-history model of src/replays-closed-history.lisp (#362).
;; page-budget-is-not-max now lives in tests/acceptance.lisp over the
;; closed-history model of src/replays-closed-history.lisp (nova-tools #362).
;; default-window-opens-two-days now lives in tests/acceptance.lisp over the
;; closed-history model of src/replays-closed-history.lisp (nova-tools #362).
;; busy-day-many-segments now lives in tests/acceptance.lisp over the
;; closed-history model of src/replays-closed-history.lisp (nova-tools #362).
;;
;; one-revision-publishes-together, index-replayed-after-crash,
;; overlay-is-bounded-and-rebuilt, absent-day-is-not-a-gap and
;; missing-segment-is-a-gap now assert their sentences in
;; slice-09-replays-publication.lisp.
;;
;; as-of-refuses-unavailable-partition now lives in
;; tests/acceptance/slice-09-replays-8603.lisp over the as-of ask of
;; src/closed-history.lisp (nova-tools #362).
;; indivisible-record-refused-before-ack now lives in
;; tests/acceptance/slice-09-replays-8603.lisp over the admission gate of
;; src/closed-history.lisp (nova-tools #362).

;;; clip-names-the-index-that-overflowed: the real replay lives in
;;; tests/acceptance/slice-09-replays-8603.lisp:81 (nova-tools #362).

;; new-verbs-have-a-kind-and-a-field-order now runs in
;; tests/acceptance/slice-05-durable-journal.lisp against src/new-verbs.lisp.

;; new-verbs-retry-to-one-event now runs in
;; tests/acceptance/slice-05-durable-journal.lisp against src/new-verbs.lisp.
;;;; ------------------------------------------------------------------
;;;; COW and closed-history replays (SPEC-WORK.md:1200-2400), named and
;;;; added for CARD-273 / #362. Green where slice-1 kernel behaviour can
;;;; carry the sentence; ;; NEEDS-KERNEL where the verb lives outside it.
;;;; ------------------------------------------------------------------

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
      (check-equal (state-closed-rows (kernel-state k)) (state-closed-rows rebuilt) "rows retained")))
    ;; `verify` over the item prints the same VERIFY ROW verdicts it printed
    ;; while the item was open, and the evidence set and its fields are read the
    ;; same before and after (SPEC-WORK.md:5649-5654). The cache holds the raw
    ;; resolution, never a verdict, so the second pass fetches nothing.
    (let* ((k2 (fresh))
           (cache (make-verification-cache))
           (calls (list 0))
           (resolver (make-verification-resolver
                      "test" "resolver-cmd"
                      :function (lambda (pointer subject)
                                  (declare (ignore pointer subject))
                                  (incf (first calls))
                                  (values :holds "2026-09-14T12:00:00Z"))))
           (session (make-verification-session
                     :cache cache :resolvers (list resolver)
                     :source-revision "gen-4"))
           (evidence (list (make-verify-evidence
                            "ev-1" :pointer "test:acme/work/t1@gen-4"
                            :criterion :test :subject "acme/work/t1"
                            :against "gen-4" :generation "gen-4"
                            :node "acme/work/f1/t1"))))
      (multiple-value-bind (open-line open-rows open-exit)
          (verify session evidence :node "acme/work/f1/t1")
        (declare (ignore open-line))
        (check-equal 0 open-exit "verify while the item is open exits 0")
        (check-equal 1 (length open-rows) "one VERIFY ROW per evidence event")
        (check-equal 1 (first calls) "the resolver ran once while the item was open")
        (ok (submit k2 (close-request :request "ski-2")) "verify fixture close refused")
        ;; The item is settled; the evidence events and their five fields read
        ;; the same from the closed index, and verify prints exactly the rows it
        ;; printed while the item was open.
        (multiple-value-bind (closed-line closed-rows closed-exit)
            (verify session evidence :node "acme/work/f1/t1")
          (check-equal open-rows closed-rows
                       "the VERIFY ROW verdicts are unchanged by the settle")
          (check-equal open-exit closed-exit
                       "the verdict count is unchanged by the settle")
          (check-equal 1 (first calls)
                       "the second verify fetched nothing: the cache answered")
          (check-equal 1 (verification-cache-size cache)
                       "the cache holds one raw fact, never a verdict")
          (ok (search "verdict=verified" (first closed-rows))
              "the cached fact still qualifies: ~A" (first closed-rows))
          (ok (search "cached=1" closed-line)
              "the cached fact answered the closed item: ~A" closed-line)))))

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

;; closed-row-with-archive-absent now lives in tests/acceptance.lisp over the
;; closed-history model of src/replays-closed-history.lisp (nova-tools #362).
;; closed-paged-without-full-load now lives in tests/acceptance.lisp over the
;; closed-index paging of src/replays-closed-history.lisp (nova-tools #362).

(deftest "days-merge-by-revision-never-concatenate" "docs/SPEC-WORK.md:5998"
    "expected=backdated-closure-in-earlier-day;from-to-prints-revision-order-across-boundary;one-batch-and-ten-yield-identical-leaves"
  (let* ((rows (list (history-row "2026-09-14" 100 "T-100")
                     (history-row "2026-09-15" 50  "T-50")
                     (history-row "2026-09-14" 200 "T-200")))
         (h (make-closed-history :rows rows :page-records 2 :root "rev-root-1")))
    ;; a closure backdated into an earlier day keeps its row in that day.
    (let ((backdated (find 200 (merge-days-by-revision h '("2026-09-14" "2026-09-15"))
                           :key (lambda (r) (getf r :revision)))))
      (check-equal "2026-09-14" (getf backdated :day)
                   "the backdated closure stays in its recorded day"))
    ;; a --from/--to over both days prints rows in revision order across the
    ;; day boundary, never one day concatenated after the other.
    (check-equal '(50 100 200)
                 (mapcar (lambda (r) (getf r :revision))
                         (merge-days-by-revision h '("2026-09-14" "2026-09-15")))
                 "rows merge by revision across the day boundary")
    ;; the same day clipped in one batch and in ten yields identical leaves.
    (let ((one (clip-day rows 2))
          (ten (clip-day-batched (list (subseq rows 0 1)
                                       (subseq rows 1 2)
                                       (subseq rows 2 3))
                                 2)))
      (check-equal one ten "one batch and ten yield the same leaves")
      (check-equal one (clip-day (reverse rows) 2)
                   "clip order does not change the leaves"))))

(deftest "page-budget-is-not-max" "docs/SPEC-WORK.md:6001"
    "expected=shown=0;pages=<n>;whole-history-never-scanned;max-bounds-rows-not-pages;continuation-same-root;other-root-page-expired"
  (let* ((rows (loop for i from 1 to 40
                     collect (history-row "2026-09-14" i (format nil "T-~D" i))))
         (h (make-closed-history :rows rows :page-records 4 :root "root-1"))
         (leaves (length (clip-day rows 4)))
         (reject (constantly nil))
         (res (query-history h :from "2026-09-14" :to "2026-09-15"
                             :filter reject :page-budget 3 :max 5)))
    ;; the filter rejects every row read: shown=0 at the budget.
    (check-equal 0 (length (query-result-shown res)) "shown=0")
    (check-equal 3 (query-result-pages res) "pages=<n> is the budget")
    (ok (query-result-more res) "the answer is MORE")
    (ok (search "shown=0" (query-result-line res)) "the line prints shown=0")
    (ok (search "pages=3" (query-result-line res)) "the line prints pages=<n>")
    ;; the whole history is never scanned.
    (ok (< (query-result-pages res) leaves)
        "only the budgeted pages are read, not the whole history")
    ;; --max caps the rows printed and bounds nothing else: a tiny --max does
    ;; not move the pages the budget read.
    (let ((small-max (query-history h :from "2026-09-14" :to "2026-09-15"
                                    :filter reject :page-budget 3 :max 1)))
      (check-equal 3 (query-result-pages small-max)
                   "--max bounds the rows, never the pages")
      (check-equal 0 (length (query-result-shown small-max))
                   "a rejected filter prints no row even under --max"))
    ;; the continuation answers from the same captured root.
    (let ((cont (continue-query h (query-result-cursor res)
                                :filter reject :page-budget 3)))
      (check-equal 3 (query-result-pages cont)
                   "the continuation reads the next budgeted pages"))
    ;; a continuation against a different root is refused page expired.
    (let ((other (make-closed-history :rows rows :page-records 4 :root "root-2")))
      (ok (search "page expired"
                  (query-result-line (continue-query other (query-result-cursor res))))
          "a continuation on another root is refused page expired"))))

(deftest "default-window-opens-two-days" "docs/SPEC-WORK.md:5561"
    "expected=at-most-two-day-partitions;midnight=one;no-partition-older-than-window;--from=only-days-holding-records"
  (let* ((rows (list (history-row "2026-09-13" 1 "T-old")
                     (history-row "2026-09-14" 2 "T-y")
                     (history-row "2026-09-15" 3 "T-t")))
         (h (make-closed-history :rows rows :page-records 1 :root "win-root")))
    ;; at early morning and at midday the rolling [now-24h, now) opens the two
    ;; UTC day partitions it intersects, today's and yesterday's.
    (dolist (now '("2026-09-15T03:00:00Z" "2026-09-15T12:00:00Z"))
      (let ((days (window-days h :now now)))
        (check-equal '("2026-09-14" "2026-09-15") days
                     "the default window opens today and yesterday")
        (ok (<= (length days) 2) "at most two day partitions")))
    ;; at exactly 00:00:00Z the interval is yesterday's whole day: one partition.
    (check-equal '("2026-09-14") (window-days h :now "2026-09-15T00:00:00Z")
                 "at midnight the window opens exactly one partition")
    ;; no partition older than the window is opened for it.
    (ok (every (lambda (d) (string>= d "2026-09-14"))
               (window-days h :now "2026-09-15T12:00:00Z"))
        "no partition older than the window is opened")
    ;; an explicit --from reaching back a month opens exactly the days in range
    ;; that hold closure records, and prints pages= for them.
    (let ((days (window-days h :from "2026-08-15" :to "2026-09-16")))
      (check-equal '("2026-09-13" "2026-09-14" "2026-09-15") days
                   "--from opens exactly the days holding closure records")
      (check-equal (length days)
                   (query-result-pages
                    (query-history h :from "2026-08-15" :to "2026-09-16"))
                   "the historical listing prints pages= for the days opened"))))

(deftest "history-grows-startup-does-not" "docs/SPEC-WORK.md:5568"
    "expected=startup-resident-bytes-flat;segment-bytes-read-flat;parses-flat;replays-flat;emitted-bytes-flat;pages-bounded-by-depth;no-whole-C-load;no-directory-scan;no-whole-dedup-load"
  (flet ((history (old-days)
           (make-closed-history
            :rows (append
                   (loop for i from 1 to 12
                         collect (history-row "2026-09-14" i (format nil "T-w~D" i)))
                   (loop for d from 1 to old-days
                         collect (history-row
                                  (format nil "2020-01-~2,'0D" (1+ (mod (1- d) 28)))
                                  d (format nil "T-old~D" d))))
            :page-records 4 :root "grow-root")))
    (let ((small (history-cost (history 0) :now "2026-09-15T12:00:00Z"))
          (huge  (history-cost (history 5000) :now "2026-09-15T12:00:00Z")))
      ;; O, the recent-window volume and the page bounds held fixed while the old
      ;; history grows by orders of magnitude: these five do not move.
      (check-equal (history-cost-startup-resident-bytes small)
                   (history-cost-startup-resident-bytes huge)
                   "startup resident bytes do not move")
      (check-equal (history-cost-segment-bytes-read small)
                   (history-cost-segment-bytes-read huge)
                   "segment bytes read do not move")
      (check-equal (history-cost-parses small) (history-cost-parses huge)
                   "parses do not move")
      (check-equal (history-cost-replays small) (history-cost-replays huge)
                   "replays do not move")
      (check-equal (history-cost-emitted-bytes small)
                   (history-cost-emitted-bytes huge)
                   "emitted bytes do not move")
      ;; index pages read stay bounded by the index depth.
      (ok (<= (history-cost-index-pages-read huge) (index-depth huge))
          "index pages read stay bounded by the index depth")
      ;; no whole-C load, no directory scan, no whole-history dedup load.
      (ok (< (history-cost-segment-bytes-read huge) 5012)
          "no whole-C load on the ordinary path")
      (check-equal 0 (history-cost-scanned-days huge) "no directory scan")
      (check-equal 0 (history-cost-dedup-loads huge)
                   "no whole-history dedup load"))))

(deftest "closed-row-with-archive-absent" "docs/SPEC-WORK.md:5541"
    "expected=same-four-rows;gap=;QUERY-NOTE-coverage-gap"
  (let* ((k (closed-four-kernel))
         (state (kernel-state k)))
    (multiple-value-bind (rows line) (nova-work::closed-ask state :archive nil)
      (check-equal 4 (length rows) "the four index rows answer with the archive absent")
      (ok (search "gap=0" line) "an unreached body is not a gap: ~A" line))
    (multiple-value-bind (rows line)
        (nova-work::closed-ask state :archive nil :reach-bodies t)
      (check-equal 4 (length rows) "still four rows, never a shorter list")
      (ok (search "gap=4" line) "gap counts the missing bodies: ~A" line)
      (ok (search "QUERY NOTE coverage-gap" line)
          "one coverage-gap note, not a shorter list: ~A" line))))

(deftest "closed-paged-without-full-load" "docs/SPEC-WORK.md:5538"
    "expected=shown=20;MORE;after=;parses=0;replays=0;history-never-loaded"
  (let* ((k (settled-kernel 1000 :spare t))
         (state (kernel-state k))
         (rows2 nil))
    (multiple-value-bind (rows more cursor line parses replays visits)
        (instrumented-closed-page state :max 20)
      (check-equal 20 (length rows) "the first page reads twenty rows")
      (ok more "the first page prints MORE")
      (check-equal 1000 (nova-work::closed-cursor-rev cursor) "the cursor pins rev 1000")
      (ok (search "after=" line) "MORE names --after: ~A" line)
      (ok (search "parses=0 replays=0" line) "the page reads and replays nothing: ~A" line)
      (check-equal 0 parses "no parse")
      (check-equal 0 replays "no replay")
      (check-equal 0 visits "no node visited: the whole history was never loaded")
      (multiple-value-bind (r2 more2 cursor2 line2)
          (instrumented-closed-page state :max 20 :cursor cursor)
        (declare (ignore more2 cursor2))
        (setf rows2 r2)
        (check-equal 20 (length r2) "the next page reads the next twenty")
        (let ((k1 (mapcar (lambda (r) (getf r :key)) rows))
              (k2 (mapcar (lambda (r) (getf r :key)) r2)))
          (ok (null (intersection k1 k2 :test #'string=)) "no row twice across pages")
          (check-string= "981:t981" (car (last k1)) "the first page ends at rev 981")
          (check-string= "980:t980" (car k2) "the second page starts at the next row"))
        (ok (search "parses=0 replays=0" line2)
            "the second page reads and replays nothing"))
    ;; a settle between two pages is excluded by the pinned cursor: the pinned
    ;; page neither repeats nor drops a row and shows no row the cursor did not
    ;; pin (SPEC-WORK.md:5538-5540).
    (multiple-value-bind (sok sline scode)
        (submit k (close-request :node "t1001" :request "late-1"))
      (declare (ignore scode))
      (ok sok "a settle after the first page is accepted: ~A" sline))
    (multiple-value-bind (rows3 more3 cursor3 line3)
        (instrumented-closed-page state :max 20 :cursor cursor)
      (declare (ignore more3 cursor3))
      (check-equal 20 (length rows3) "the pinned page still reads twenty rows")
      (check-equal (mapcar (lambda (r) (getf r :key)) rows2)
                   (mapcar (lambda (r) (getf r :key)) rows3)
                   "the pinned page neither repeats nor drops a row")
      (ok (notany (lambda (r) (string= "t1001" (getf r :node))) rows3)
          "the settle after the pin is not shown in the pinned page: ~A" line3))
    ;; a cursor whose pinned revision is below the floor is refused `page expired`.
    (multiple-value-bind (rows4 more4 cursor4 line4)
        (instrumented-closed-page state :max 20 :cursor cursor :floor 1005)
      (declare (ignore more4 cursor4))
      (check-equal nil rows4 "an expired page reads no rows")
      (ok (search "page expired" line4) "the refusal names the expiry: ~A" line4)))))
