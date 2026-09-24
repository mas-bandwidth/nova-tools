;;;; E10-F04-03-engine.lisp --- the nova-work engine side of the E10-F04-03
;;;; pilot (docs/SPEC-WORK.md:7228-7229, stage 4), driven by E10-F04-03-run.sh.
;;;;
;;;; import: read the stage-3 capture (index.tsv + records/) into a nova-work
;;;;   state, one node per captured record under the repository's work-set,
;;;;   and write it with the engine's own export writer (`write-state-export`)
;;;;   into a disposable directory. LIMIT stops after that many new records (an
;;;;   interrupted import); FROM resumes: it loads the earlier partial export
;;;;   through `state-load` and imports only the records it lacks.
;;;; load: in a FRESH engine (a separate SBCL process with an empty
;;;;   environment), load the export through the isolated read-only
;;;;   `state-load`, rebuild the model with `read-loaded-snapshot`, and print
;;;;   its own inventory -- each record's hash computed here from the loaded
;;;;   bytes -- plus the load line and the re-export digest, so the caller
;;;;   compares the destination to the capture independently of the importer.
;;;;
;;;; usage (after loading the nova-work system):
;;;;   (nova-work-pilot:main '("import" CAPTURE EXPORT-DIR REPO [LIMIT [FROM]]))
;;;;   (nova-work-pilot:main '("load" EXPORT-DIR INTO-DIR REPO))

(defpackage #:nova-work-pilot
  (:use #:cl)
  (:export #:main))

(in-package #:nova-work-pilot)

(defun slurp (path)
  (nova-work::%state-load-octets-string (nova-work::%state-load-read-octets path)))

(defun split-tab (line)
  (loop with start = 0
        for tab = (position #\Tab line :start start)
        collect (subseq line start tab)
        while tab do (setf start (1+ tab))))

(defun read-index (capture)
  "The capture's index rows: (id kind mapping sha256)."
  (with-open-file (in (format nil "~A/index.tsv" capture) :external-format :utf-8)
    (loop for line = (read-line in nil) while line
          when (plusp (length line)) collect (split-tab line))))

(defun record-spec (repo id kind mapping text)
  (list :id (format nil "~A/~A" repo id) :type :task :parent repo :state :unknown
        :title text :category kind :links (list mapping)
        :version (nova-work:sha256-hex text)))

(defun loaded-state (export-dir into)
  (multiple-value-bind (snap line) (nova-work:state-load :from export-dir :into into
                                                          :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)
    (unless snap (error "~A" line))
    (values (nova-work:snapshot-state (nova-work:read-loaded-snapshot into :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)) line)))

(defun record-nodes (state repo)
  "The loaded record nodes, in seed order: (record-id kind mapping text version)."
  (loop for id in (nova-work::wstate-order state)
        for n = (gethash id (nova-work::wstate-nodes state))
        when (equal repo (nova-work::wnode-parent n))
          collect (list (subseq id (1+ (length repo)))
                        (nova-work::wnode-category n)
                        (first (nova-work::wnode-links n))
                        (nova-work::wnode-title n)
                        (nova-work::wnode-version n))))

(defun do-import (capture export-dir repo limit from)
  (let ((have (when from
                (record-nodes (loaded-state from (format nil "~A.resume-load/"
                                                        (string-right-trim "/" from)))
                              repo)))
        (seed (list (list :id repo :type :work-set :parent nil :state :unknown)))
        (new 0) (kept 0))
    (dolist (row (read-index capture))
      (destructuring-bind (id kind mapping sha) row
        (let ((prior (assoc id have :test #'string=)))
          (cond
            (prior (incf kept)
                   (push (record-spec repo id kind mapping (fourth prior)) seed))
            ((and (plusp limit) (>= new limit)) (return))
            (t (let ((text (slurp (format nil "~A/records/~A" capture id))))
                 (unless (string= sha (nova-work:sha256-hex text))
                   (error "IMPORT FAIL: ~A hashes to ~A, the capture says ~A"
                          id (nova-work:sha256-hex text) sha))
                 (incf new)
                 (push (record-spec repo id kind mapping text) seed)))))))
    (multiple-value-bind (manifest manifest-sha member)
        (nova-work:write-state-export (nova-work:make-seed-state (reverse seed))
                                      export-dir :id "E10-F04-03-import")
      (declare (ignore member))
      (format t "IMPORT OK new=~D kept=~D manifest=~A member=~A~%" new kept manifest-sha
              (getf (first (getf manifest :members)) :sha256)))))

(defun do-load (export-dir into repo)
  (multiple-value-bind (state line) (loaded-state export-dir into)
    (format t "LOAD~C~A~%" #\Tab line)
    (dolist (r (record-nodes state repo))
      (destructuring-bind (id kind mapping text version) r
        (let ((sha (nova-work:sha256-hex text)))
          (format t "REC~C~A~C~A~C~A~C~A~C~A~%" #\Tab id #\Tab kind #\Tab mapping #\Tab sha
                  #\Tab (if (equal sha version) "hash-ok" "hash-mismatch")))))
    (format t "REEXPORT~C~A~%" #\Tab
            (nova-work:sha256-hex (nova-work::canonical-string
                                   (nova-work::state-canonical-form state))))))

(defun main (args)
  (destructuring-bind (verb &rest rest) args
    (cond
      ((string= verb "import")
       (destructuring-bind (capture export-dir repo &optional (limit "0") from) rest
         (do-import capture export-dir repo (parse-integer limit) from)))
      ((string= verb "load")
       (destructuring-bind (export-dir into repo) rest
         (do-load export-dir into repo)))
      (t (error "unknown verb ~A" verb)))))
