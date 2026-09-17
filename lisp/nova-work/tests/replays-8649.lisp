;;;; replays-8649.lisp --- the five acceptance replays of card 8649: the
;;;; roadmap proof, one-journal rotation, rule 2's unavailable partition, the
;;;; savepoint's cut and its write-failure retention. Each deftest names the
;;;; paragraph of docs/SPEC-WORK.md it comes from and the required outcome.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; roadmap-proof                              SPEC-WORK.md:6244
;;; ------------------------------------------------------------------

(deftest "roadmap-proof" "docs/SPEC-WORK.md:6244"
    "expected=chat-and-file-renders-identical;optional-axes;partial-and-stale-evidence;shared-prerequisites;discovered-scope;closed-members;marker-edit-preserves-bytes-and-refuses-ambiguity"
  (let* ((rows (list (make-roadmap-row :id "f-1" :axis :feature :kind :required
                                       :state :open :evidence :partial :status :stale)
                     (make-roadmap-row :id "t-1" :axis :task :kind :optional
                                       :state :open :evidence :none :status :current)
                     (make-roadmap-row :id "c-1" :axis :task :kind :required
                                       :state :closed :evidence :full :status :current)))
         (rm (make-roadmap :id "rm-1" :axes '(:feature :task) :rows rows
                           :shared-prerequisites '("dep-shared")
                           :discovered '("disc-1")
                           :closed '("c-1"))))
    ;; chat and file renders identical.
    (check-string= (roadmap-render rm :chat) (roadmap-render rm :file)
                   "chat and file renders are identical")
    (let ((text (roadmap-render rm :chat)))
      (ok (search "axis=feature" text) "the first axis is rendered")
      (ok (search "axis=task" text) "the optional axis is rendered")
      (ok (search "f-1" text) "the required member is rendered")
      (ok (search "t-1" text) "the optional member is rendered")
      (ok (search "evidence=partial" text) "partial evidence is visible")
      (ok (search "status=stale" text) "stale evidence is visible")
      (ok (search "shared-prerequisite dep-shared" text) "a shared prerequisite is named")
      (ok (search "discovered disc-1" text) "newly discovered scope is named")
      (ok (search "closed c-1" text) "a closed member is named"))
    ;; a unique marker edits that byte range and preserves every unrelated byte.
    (multiple-value-bind (edited line) (roadmap-edit-marker "alpha\nMARK\nomega\n" "MARK" "done")
      (check-equal nil line "a unique marker edits cleanly")
      (check-string= "alpha\ndone\nomega\n" edited "only the marker bytes change"))
    ;; an ambiguous marker refuses and publishes nothing.
    (multiple-value-bind (edited line) (roadmap-edit-marker "M\nM\n" "M" "x")
      (check-equal nil edited "an ambiguous marker publishes nothing")
      (ok (search "ambiguous" line) "the ambiguity is named: ~A" line))
    ;; an absent marker refuses.
    (multiple-value-bind (edited line) (roadmap-edit-marker "abc\n" "M" "x")
      (check-equal nil edited "an absent marker publishes nothing")
      (ok (search "not found" line) "the absence is named: ~A" line))))

;;; ------------------------------------------------------------------
;;; rotation-keeps-one-journal               SPEC-WORK.md:5998
;;; ------------------------------------------------------------------

(deftest "rotation-keeps-one-journal" "docs/SPEC-WORK.md:5998"
    "expected=one-journal-id;copied-boundary-same-seq-and-hash;chain-continues;cut-reachable-without-old-scan;export-identical-from-either-file"
  (let* ((rec1 (make-journal-record :seq 1 :events '("e1")))
         (rec2 (make-journal-record :seq 2 :events '("e2")))
         (old (make-journal-segment :path "journal.1" :header '(:segment 1)
                                    :records (list rec1 rec2)))
         (chain (make-journal-chain :id "journal-abc" :segments (list old)))
         (rotated (rotate-journal chain))
         (new (car (last (journal-chain-segments rotated))))
         (boundary (getf (journal-segment-header new) :copied-boundary)))
    ;; the new segment's header names the same journal id.
    (check-equal "journal-abc" (getf (journal-segment-header new) :journal-id)
                 "the new segment header names the same journal id")
    ;; the copied boundary record carries its sequence and hash.
    (check-equal 2 (getf boundary :seq) "the boundary record keeps its sequence")
    (check-string= (journal-record-hash rec2) (getf boundary :sha256)
                   "the boundary record keeps its hash")
    (check-string= "journal.1" (getf (journal-segment-header new) :copied-from)
                   "the boundary names the segment it was copied from")
    ;; the chain continues past the copied boundary.
    (let* ((rotated2 (journal-append-record rotated (make-journal-record :events '("e3"))))
           (new2 (car (last (journal-chain-segments rotated2)))))
      (check-equal 3 (journal-record-seq (car (last (journal-segment-records new2))))
                   "the chain continues past the boundary")
      (check-equal 2 (journal-record-seq (first (journal-segment-records new2)))
                   "the copied boundary record is the first record of the new segment"))
    ;; the savepoint cut is reachable from the new segment's boundary record alone.
    (check-equal t (savepoint-cut-reachable-p rotated 2)
                 "the savepoint cut is reachable from the boundary record")
    ;; export --journal writes the same bundle from either file.
    (check-string= (export-journal-bundle rotated :from "journal.1")
                   (export-journal-bundle rotated :from "journal.2")
                   "the same bundle is written from either file")))

;;; ------------------------------------------------------------------
;;; rule-2-unavailable-is-not-green          SPEC-WORK.md:5024/5633
;;; ------------------------------------------------------------------

(deftest "rule-2-unavailable-is-not-green" "docs/SPEC-WORK.md:5033"
    "expected=unavailable-partition-is-a-finding-at-exit-1;distinct-from-dangling;never-green-over-unread-history"
  (let ((ref '(:id "n-9" :partition "2026-09-01" :resolved-p nil)))
    ;; a partition that cannot be read is `unavailable`, a finding at exit 1.
    (multiple-value-bind (verdict line) (rule-2-check ref :partition-readable-p nil)
      (check-equal :unavailable verdict "an unread partition is unavailable")
      (ok (search "rule 2: unavailable partition=2026-09-01" line)
          "the finding names the partition: ~A" line))
    ;; it is distinct from `dangling`.
    (multiple-value-bind (verdict line) (rule-2-check ref :partition-readable-p t)
      (check-equal :dangling verdict "a readable partition with no node is dangling")
      (ok (search "dangling" line) "the dangling finding is named: ~A" line))
    (check-equal nil (equal :unavailable :dangling)
                 "incomplete and invalid are different answers"))
  ;; a resolved reference is green; a green rule prints WORK OK, an unavailable
  ;; rule prints WORK FAIL at exit 1 and never green over unread history.
  (let ((good (rule-2-check '(:id "n-1" :partition "2026-09-01" :resolved-p t)
                            :partition-readable-p nil))
        (bad (rule-2-check '(:id "n-9" :partition "2026-09-01" :resolved-p nil)
                           :partition-readable-p nil)))
    (check-equal :green good "a resolved reference is green")
    (multiple-value-bind (code line) (validate-report (list good))
      (check-equal 0 code "an all-green validation exits 0")
      (ok (search "WORK OK" line) "green prints WORK OK: ~A" line))
    (multiple-value-bind (code line) (validate-report (list bad))
      (check-equal 1 code "an unavailable finding exits 1")
      (ok (search "WORK FAIL" line) "a finding prints WORK FAIL: ~A" line)
      (ok (not (search "WORK OK" line)) "green is never printed over unread history"))))

;;; ------------------------------------------------------------------
;;; savepoint-cut-never-splits-an-envelope   SPEC-WORK.md:5989
;;; ------------------------------------------------------------------

(deftest "savepoint-cut-never-splits-an-envelope" "docs/SPEC-WORK.md:5989"
    "expected=two-events-once-in-the-image;retry-byte-identical;cut-inside-an-envelope-refused;no-image-published"
  (let* ((records (list (list :request "r1" :reply '("OK r1") :events '(1 2))))
         (store (make-savepoint-store)))
    ;; a savepoint after a two-event request: both events once in the image.
    (multiple-value-bind (s line) (savepoint-write store "sp-1" 2 2 records)
      (check-equal nil line "a cut at a record boundary publishes")
      (let* ((image (first (savepoint-store-published s)))
             (events (getf image :events)))
        (check-equal '(1 2) events "both events are represented once")
        (check-equal 2 (length events) "no event is duplicated")
        (check-string= "OK r1"
                       (first (cdr (assoc "r1" (getf image :replies) :test #'equal)))
                       "a retry is answered the byte-identical original line")))
    ;; a cut between the two events is refused and no image is published.
    (multiple-value-bind (s line) (savepoint-write store "sp-2" 2 1 records)
      (ok (search "cut inside an envelope" line) "the refusal is named: ~A" line)
      (check-equal nil (savepoint-store-published s) "no image is published"))))

;;; ------------------------------------------------------------------
;;; savepoint-write-failure-keeps-the-previous SPEC-WORK.md:6014
;;; ------------------------------------------------------------------

(deftest "savepoint-write-failure-keeps-the-previous" "docs/SPEC-WORK.md:6014"
    "expected=previous-verified-restores;attempt-verdict-failed"
  (let* ((records (list (list :request "r1" :reply '("OK r1") :events '(1))))
         (base (make-savepoint-store :verified '(:id "sp-prev" :rev 1))))
    ;; a clean write publishes a new verified savepoint.
    (multiple-value-bind (s line) (savepoint-write base "sp-new" 2 1 records)
      (check-equal nil line "a clean savepoint writes")
      (check-equal "sp-new" (getf (savepoint-store-verified s) :id)
                   "the new savepoint is verified"))
    ;; the image write, the manifest publication and the sync each failed in
    ;; turn: the previous verified savepoint restores and the attempt is listed.
    (dolist (stage '(:image :manifest :sync))
      (multiple-value-bind (s line) (savepoint-write base "sp-new" 2 1 records :fail-stage stage)
        (ok (search "SAVEPOINT FAIL" line) "a failed write reports: ~A" line)
        (check-equal "sp-prev" (getf (savepoint-store-verified s) :id)
                     "the previous verified savepoint restores after a failed write")
        (check-equal nil (savepoint-store-published s) "no new image is published")
        (ok (search "verdict=failed" (savepoint-list s))
            "savepoint list prints the attempt verdict=failed")))))
