;;;; storage-split.lisp --- nova-tools#3174 part (i): the storage split, as
;;;; engine code, proven on the invented ingest-30 fixture.
;;;;
;;;;   storage-split-digests        SPEC-WORK.md section 9 "The storage split"
;;;;   storage-split-reload-agrees  SPEC-WORK.md section 9 "The storage split"
;;;;
;;;; tests/fixtures/ingest-30/ is the fixture every part of #3174 shares
;;;; (section 10): an invented map (map.sexp) and the record the engine writes
;;;; from it -- the manifest work.sexp, one O and one C file per repository
;;;; under work/, and the bodies under blobs/. It is invented data of an
;;;; invented org; no record path of the work repo is written here.
;;;;
;;;; Every assertion reads the bytes back off the disk with its own reads, so a
;;;; digest is checked against the file as written, never against the string
;;;; the writer meant to write.

(in-package #:nova-work/tests)

(defparameter +ss-bounds+ '(:max-bytes 1048576 :max-depth 32 :max-nodes 200000))

(defun ss-fixture-dir ()
  (string-right-trim "/" (namestring (asdf:system-relative-pathname
                                      :nova-work "tests/fixtures/ingest-30/"))))

(defun ss-load (root)
  (apply #'nova-work::load-work-split root +ss-bounds+))

(defun ss-octets (path)
  (with-open-file (in path :element-type '(unsigned-byte 8))
    (let ((bytes (make-array (file-length in) :element-type '(unsigned-byte 8))))
      (read-sequence bytes in)
      bytes)))

(defun ss-path (root relative) (format nil "~A/~A" (string-right-trim "/" (namestring root)) relative))

(defun ss-files (root)
  "Every regular file under ROOT as a sorted list of relative names."
  (let ((out '()))
    (labels ((walk (dir prefix)
               (dolist (f (uiop:directory-files dir))
                 (push (concatenate 'string prefix (file-namestring f)) out))
               (dolist (sub (uiop:subdirectories dir))
                 (walk sub (concatenate 'string prefix (car (last (pathname-directory sub))) "/")))))
      (walk (uiop:ensure-directory-pathname root) ""))
    (sort out #'string<)))

(defun ss-map-repositories ()
  "The fixture map's declared repositories, <org>/<repo>, in map order, :skip
rows left out: the manifest's :repositories come from the fixture's own
invented map (#3174 part (i) reads no ingest-map of the work repo)."
  (let ((map (apply #'nova-work::read-bounded
                    (nova-work::%split-octets-text (ss-octets (ss-path (ss-fixture-dir) "map.sexp")) "map.sexp")
                    +ss-bounds+)))
    (loop for row in (getf map :repos)
          unless (eq :skip (getf row :disposition))
            collect (format nil "~A/~A" (getf map :org) (getf row :repo)))))

(defun ss-bodies (state root)
  "The bodies STATE names, read from the blobs under ROOT."
  (let ((table (make-hash-table :test #'equal)))
    (dolist (sha (nova-work::split-body-shas state))
      (setf (gethash sha table)
            (nova-work::%split-octets-text
             (ss-octets (ss-path root (nova-work::split-blob-path sha))) sha)))
    table))

(defun ss-write (state root)
  (nova-work::write-work-split state (namestring root)
                               :repositories (ss-map-repositories)
                               :bodies (ss-bodies state (ss-fixture-dir))))

(defun ss-manifest (root)
  (apply #'nova-work::read-bounded
         (nova-work::%split-octets-text (ss-octets (ss-path root "work.sexp")) "work.sexp")
         +ss-bounds+))

(defun ss-row (manifest repo)
  (find repo (getf manifest :repositories) :key (lambda (r) (getf r :repo)) :test #'equal))

(defun ss-same-tree (expected-root actual-root what &key ignore)
  (let ((expected (set-difference (ss-files expected-root) ignore :test #'string=))
        (actual (ss-files actual-root)))
    (setf expected (sort expected #'string<))
    (check-equal expected actual (format nil "~A: the file set" what))
    (dolist (f expected)
      (unless (equalp (ss-octets (ss-path expected-root f)) (ss-octets (ss-path actual-root f)))
        (fail "~A: ~A is not byte-identical" what f)))))

(defun ss-normal-index (state)
  (let ((ix (nova-work::reconstruct-state-index state)))
    (list :open (getf ix :open) :closed (getf ix :closed) :leaf (getf ix :leaf)
          :issue (getf ix :issue)
          :containers (sort (copy-list (getf ix :containers)) #'string< :key #'car)
          :holders (nova-work::%normalize-holder-index (getf ix :holders)))))

(defun ss-node-view (state)
  "Every node's record, branch, settles, revived and needs-broken bit, by id."
  (sort (loop for id in (nova-work::wstate-order state)
              collect (let ((n (gethash id (nova-work::wstate-nodes state))))
                        (list id (nova-work::split-node-record n)
                              (nova-work::wnode-branch n) (nova-work::wnode-settles n)
                              (nova-work::wnode-revived n)
                              (and (nova-work::wnode-needs-broken n) t)
                              (nova-work::wnode-open-count n))))
        #'string< :key #'car))

(defun ss-refusal (thunk)
  (handler-case (progn (funcall thunk) nil)
    (nova-work::unsupported-input (c) (princ-to-string c))))

(deftest "storage-split-digests" "SPEC-WORK.md s9 storage split"
    "the manifest's digests are the files' bytes; a C-only change moves its repository and root digest; one blob per body"
  (let* ((fixture (ss-fixture-dir))
         (state (ss-load fixture))
         (out (test-temp-dir "split-digests"))
         (repos (ss-map-repositories)))
    (check-equal '("acme/engine" "acme/tools" "acme/ideas") repos "the fixture map's declared repositories")
    (check-equal '() (nova-work::state-index-mismatches state) "the loaded counters against a reconstruction")
    (check-equal 37 (length (nova-work::wstate-order state)) "nodes: 30 units and 7 containers")
    (check-equal 8 (nova-work::state-closed-count state) "|C| of the fixture")
    ;; The engine writes ingest-30 as the split, and what it writes IS the fixture.
    (ss-write state out)
    (ss-same-tree fixture (ss-path out "") "ingest-30 rewritten" :ignore '("map.sexp"))
    (let ((manifest (ss-manifest out)) (repo-digests '()))
      ;; One :repositories row per fixture repository, in the map's order.
      (check-equal repos (mapcar (lambda (r) (getf r :repo)) (getf manifest :repositories))
                   "the manifest's :repositories rows")
      (dolist (row (getf manifest :repositories))
        (let* ((repo (getf row :repo))
               (o (ss-octets (ss-path out (getf row :file))))
               (c (ss-octets (ss-path out (getf row :closed)))))
          (check-string= (format nil "work/~A.sexp" repo) (getf row :file) "the O path")
          (check-string= (format nil "work/~A.closed.sexp" repo) (getf row :closed) "the C path")
          (check-string= (nova-work::sha256-hex o) (getf row :file-digest)
                         (format nil "~A :file-digest against the O bytes on disk" repo))
          (check-string= (nova-work::sha256-hex c) (getf row :closed-digest)
                         (format nil "~A :closed-digest against the C bytes on disk" repo))
          (check-string= (nova-work::sha256-hex (concatenate 'string (getf row :file-digest)
                                                            (getf row :closed-digest)))
                         (getf row :digest) (format nil "~A :digest, file then closed" repo))
          (push (getf row :digest) repo-digests)))
      (check-string= (nova-work::sha256-hex (apply #'concatenate 'string (reverse repo-digests)))
                     (getf manifest :digest) "the root :digest over the repository digests")
      ;; A repository with no closed rows has an empty C file and its hash.
      (let ((ideas (ss-row manifest "acme/ideas")))
        (check-equal 0 (length (ss-octets (ss-path out (getf ideas :closed)))) "acme/ideas C file bytes")
        (check-string= "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
                       (getf ideas :closed-digest) "acme/ideas :closed-digest"))
      ;; Two units with the same body store one blob.
      (let* ((shas (loop for n in '(10 11)
                         collect (getf (first (getf (nova-work::wnode-extra
                                                     (gethash (format nil "gh/acme/engine/~D" n)
                                                              (nova-work::wstate-nodes state)))
                                                    :correspondence))
                                       :body-sha256)))
             (blobs (remove-if-not (lambda (f) (eql 0 (search "blobs/" f))) (ss-files (ss-path out ""))))
             (units (remove-if-not (lambda (id) (eql 0 (search "gh/" id))) (nova-work::wstate-order state))))
        (check-string= (first shas) (second shas) "engine#10 and engine#11 carry one body")
        (check-equal 30 (length units) "units with a body")
        (check-equal 29 (length blobs) "blob files: one per distinct body")
        (check-equal 1 (count (nova-work::split-blob-path (first shas)) blobs :test #'string=)
                     "the shared body's blob")
        (dolist (f blobs)
          (check-string= (subseq f (1+ (position #\/ f :from-end t)))
                         (nova-work::sha256-hex (ss-octets (ss-path out f)))
                         (format nil "~A is named by its content" f))))
      ;; A C-only change: a settle appends one row to acme/tools' C file and
      ;; leaves its O file byte-identical; the repository and root digests move
      ;; with it, and no other repository's digests do.
      (let* ((changed (ss-load out))
             (out2 (test-temp-dir "split-c-only")))
        (nova-work::apply-event
         changed
         (nova-work::make-work-event :kind :settle :node "gh/acme/tools/1" :by "rowan"
                                    :fields (list :disposition :cancelled :reason "fixture: C-only change"
                                                  :already-closed nova-work::+absent+)
                                    :stamp "2026-09-24T12:00:00Z" :clock :given :request "c-only"
                                    :rev (1+ (nova-work::state-revision changed))))
        (ss-write changed out2)
        (let ((after (ss-manifest out2)))
          (let ((b (ss-row manifest "acme/tools")) (a (ss-row after "acme/tools")))
            (check-string= (getf b :file-digest) (getf a :file-digest) "acme/tools :file-digest after a settle")
            (ok (equalp (ss-octets (ss-path out (getf b :file))) (ss-octets (ss-path out2 (getf a :file))))
                "acme/tools O bytes changed on a settle")
            (ok (string/= (getf b :closed-digest) (getf a :closed-digest)) "acme/tools :closed-digest did not move")
            (ok (string/= (getf b :digest) (getf a :digest)) "acme/tools :digest did not move on a C-only change")
            (ok (string/= (getf manifest :digest) (getf after :digest)) "the root :digest did not move on a C-only change"))
          (dolist (repo '("acme/engine" "acme/ideas"))
            (check-equal (ss-row manifest repo) (ss-row after repo)
                         (format nil "~A's row after a change to another repository" repo)))
          ;; The C file changed on disk alone, with the manifest left as it
          ;; was, is refused on load: the digest rule is checked, not trusted.
          (let ((path (ss-path out2 "work/acme/tools.closed.sexp")))
            (with-open-file (s path :direction :output :if-exists :append :element-type '(unsigned-byte 8))
              (write-sequence (nova-work::utf8-octets " ") s))
            (let ((refusal (ss-refusal (lambda () (ss-load out2)))))
              (ok (and refusal (eql 0 (search "WORK FAIL digest work/acme/tools.closed.sexp" refusal)))
                  "a C file changed behind the manifest was not refused: ~S" refusal))))))))

(deftest "storage-split-reload-agrees" "SPEC-WORK.md s9 storage split"
    "loading the written split reconstructs the same indexes, and writing it again is byte-identical"
  ;; The fixture, round trip.
  (let* ((original (ss-load (ss-fixture-dir)))
         (t1 (test-temp-dir "split-reload-1"))
         (t2 (test-temp-dir "split-reload-2")))
    (ss-write original t1)
    (let ((reloaded (ss-load t1)))
      (check-equal (ss-normal-index original) (ss-normal-index reloaded)
                   "reconstruct-state-index of the reloaded fixture")
      (check-equal '() (nova-work::state-index-mismatches reloaded) "the reloaded maintained counters")
      (check-equal (ss-node-view original) (ss-node-view reloaded) "every node of the reloaded fixture")
      (check-equal (nova-work::state-closed-rows original) (nova-work::state-closed-rows reloaded)
                   "the closed-index rows")
      (check-equal (nova-work::state-revision original) (nova-work::state-revision reloaded) "the revision")
      (ss-write reloaded t2)
      (ss-same-tree t1 t2 "the reloaded fixture written again")))
  ;; A state built by events, not by the loader: two repositories, a
  ;; cross-repository need that is settled then revived (so its dependent's
  ;; needs-broken bit is derived on load), a live lease, a prioritised unit.
  (let* ((state (nova-work::make-seed-state
                 (list (list :id "x/a" :type :work-set :repo "x/a" :state :todo)
                       (list :id "x/a/e" :type :epic :parent "x/a" :state :todo :title "é 雪 \"q\" \\ tab	end")
                       (list :id "x/a/1" :type :task :parent "x/a/e" :state :todo :links (list "u1"))
                       (list :id "x/a/2" :type :task :parent "x/a/e" :state :todo :deps (list "x/b/1"))
                       (list :id "x/b" :type :work-set :repo "x/b" :state :todo)
                       (list :id "x/b/1" :type :bug :parent "x/b" :state :todo :team-field (list 1 2))
                       (list :id "x/b/2" :type :task :parent "x/b" :required nil :state :todo))))
         (rev 0))
    (flet ((ev (kind node fields)
             (nova-work::apply-event
              state (nova-work::make-work-event :kind kind :node node :by "rowan" :fields fields
                                               :stamp "2026-09-24T00:00:00Z" :clock :given
                                               :request (format nil "r~D" (incf rev)) :rev rev))))
      (ev :settle "x/b/1" (list :disposition :done :reason "done" :already-closed nova-work::+absent+))
      (ev :revive "x/b/1" (list :reason "reopened"))
      (ev :settle "x/a/1" (list :disposition :done :reason "done" :already-closed nova-work::+absent+))
      (ev :lease "x/b/2" (list :change :take :holder "ada"))
      (ev :prioritise "x/a/2" (list :change :set :context :subtree :rank 3 :reason "first")))
    (ok (nova-work::wnode-needs-broken (gethash "x/a/2" (nova-work::wstate-nodes state)))
        "the built state's dependent of a revived need is not needs-broken")
    (let ((t3 (test-temp-dir "split-reload-3")) (t4 (test-temp-dir "split-reload-4")))
      (nova-work::write-work-split state (namestring t3) :repositories '("x/a" "x/b"))
      (let ((reloaded (ss-load t3)))
        (check-equal (ss-normal-index state) (ss-normal-index reloaded)
                     "reconstruct-state-index of the reloaded event-built state")
        (check-equal '() (nova-work::state-index-mismatches reloaded) "its maintained counters")
        (check-equal (ss-node-view state) (ss-node-view reloaded) "every node of the event-built state")
        (check-equal (nova-work::state-closed-rows state) (nova-work::state-closed-rows reloaded) "its closed rows")
        (nova-work::write-work-split reloaded (namestring t4) :repositories '("x/a" "x/b"))
        (ss-same-tree t3 t4 "the event-built state written again"))
      ;; Refusals: a forest outside the declared repositories, and a path
      ;; that could climb out of work/.
      (ok (search "every top-level node is a declared repository's work set"
                  (or (ss-refusal (lambda () (nova-work::write-work-split
                                              state (namestring (test-temp-dir "split-refuse"))
                                              :repositories '("x/a"))))
                      ""))
          "an undeclared repository's forest was written")
      (ok (search "WORK REFUSED repository"
                  (or (ss-refusal (lambda () (nova-work::split-repo-paths "../x"))) ""))
          "a repository name that climbs out of work/ was admitted"))))
