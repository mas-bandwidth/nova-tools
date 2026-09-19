;;;; verification-cache-file.lisp --- the verification cache's one file.
;;;;
;;;; docs/SPEC-WORK.md:1294-1325: "The cache holds raw resolutions, never
;;;; verdicts. Its key is the pointer, the subject the resolver was passed, and
;;;; the identity of the resolver that answered it. ... One session has one
;;;; cache, and this is the one sentence that says where a cache is named: the
;;;; path is `session start --cache`."
;;;;
;;;; This file is that path. A fact is written as one canonical line, the whole
;;;; cache is written atomically and stably (sorted by the line's own string so
;;;; two writes are byte identical), and a reader re-reads those lines through
;;;; the restricted reader, which never evaluates what it reads.

(in-package #:nova-work)

(defparameter +verification-fact-keys+ '(:pointer :subject :resolver :fact :stamp)
  "The five keys a written fact form carries (SPEC-WORK.md:1294-1325).")

(defun verification-fact-form (fact)
  "The plist VERIFICATION-FACT is written as (SPEC-WORK.md:1294-1325): the
pointer, the subject the resolver was passed, the resolver's identity, the raw
fact itself and the stamp it was established at."
  (list :pointer (verification-fact-pointer fact)
        :subject (verification-fact-subject fact)
        :resolver (verification-fact-resolver fact)
        :fact (verification-fact-fact fact)
        :stamp (verification-fact-stamp fact)))

(defun %verification-cache-lines (cache)
  "Every fact's one canonical line, sorted by the line's own string so two
writes of one cache are byte identical (SPEC-WORK.md:1294-1325)."
  (sort (loop for fact being the hash-values of (verification-cache-facts cache)
              collect (canonical-string (verification-fact-form fact)))
        #'string<))

(defun %rename-over (from to)
  "Rename FROM onto TO, replacing TO atomically."
  #+sbcl (sb-posix:rename (namestring from) (namestring to))
  #-sbcl (rename-file from to))

(defun write-verification-cache (cache path)
  "Write CACHE's facts to PATH, one canonical line each and sorted by the line's
own string, atomically and stably: write `PATH.new', sync it, rename it over
PATH and sync PATH's parent, so no `.new' remains and two writes of one cache
are byte identical. Returns the count written (SPEC-WORK.md:1294-1325)."
  (let* ((path (merge-pathnames path))
         (new-path (concatenate 'string (namestring path) ".new"))
         (lines (%verification-cache-lines cache)))
    (with-open-file (out new-path :direction :output :if-exists :supersede
                                  :if-does-not-exist :create
                                  :element-type 'character :external-format :utf-8)
      (dolist (line lines)
        (write-string line out)
        (write-char #\Newline out))
      (sync-stream out :path new-path))
    (%rename-over new-path path)
    (sync-directory (directory-namestring path))
    (length lines)))

(defun %verification-cache-form-p (form)
  "Whether FORM is a written fact form: a plist carrying all five keys, with a
`:fact' that is `:holds' or `:absent' (SPEC-WORK.md:1294-1325)."
  (and (listp form)
       (evenp (length form))
       (loop for (key nil) on form by #'cddr always (keywordp key))
       (loop for key in +verification-fact-keys+
             always (loop for (k nil) on form by #'cddr thereis (eq k key)))
       (member (getf form :fact) '(:holds :absent))))

(defun read-verification-cache (path &key resolvers)
  "Read the cache file PATH back into a fresh cache built with RESOLVERS
(SPEC-WORK.md:1294-1325). A PATH that does not exist is an empty cache and not
an error. Every line goes through READ-RESTRICTED; a line that does not read, a
form that is not a fact form, or a `:fact' that is neither `:holds' nor
`:absent' signals UNSUPPORTED-INPUT naming PATH and the 1-based line, and
installs nothing: the cache is returned only when every line passed."
  (let ((cache (make-verification-cache :resolvers resolvers)))
    (unless (probe-file path)
      (return-from read-verification-cache cache))
    (with-open-file (in path :direction :input :element-type 'character
                             :external-format :utf-8)
      (loop for line = (read-line in nil :eof)
            for n from 1
            until (eq line :eof)
            do (let ((form (handler-case (read-restricted line)
                             (restricted-data-violation (c)
                               (error 'unsupported-input
                                      :what (format nil "verification cache ~A line ~D does not read: ~A"
                                                    (namestring path) n c))))))
                 (unless (%verification-cache-form-p form)
                   (error 'unsupported-input
                          :what (format nil "verification cache ~A line ~D is not a fact form"
                                        (namestring path) n)))
                 (verification-cache-store cache
                                           (getf form :pointer)
                                           (getf form :subject)
                                           (getf form :resolver)
                                           (getf form :fact)
                                           (getf form :stamp)))))
    cache))

(defun resolver-identities (resolvers)
  "The canonical resolver-identity list for RESOLVERS (SPEC-WORK.md:1294-1301):
the command string of each resolver, verbatim and unnormalized. A string in the
list passes through unchanged and NIL stays NIL. The resolver identity is the
command string everywhere, and no resolver object is ever compared for identity."
  (cond ((null resolvers) nil)
        ((stringp resolvers) (list resolvers))
        (t (mapcar (lambda (resolver)
                     (if (stringp resolver)
                         resolver
                         (verification-resolver-command resolver)))
                   resolvers))))

(defun session-verification-from-cache (cache-path resolvers)
  "The verification session `session start --cache` builds over CACHE-PATH with
RESOLVERS, or nil when no non-empty cache is named (SPEC-WORK.md:1322-1325)."
  (when (and (stringp cache-path) (plusp (length cache-path)))
    (make-verification-session
     :cache (read-verification-cache cache-path
                                     :resolvers (resolver-identities resolvers))
     :cache-path cache-path
     :resolvers resolvers)))

(defun persist-verification-cache (session)
  "Persist SESSION's own cache at the path `session start --cache` named and
return the count written, or do nothing and return NIL when the session names
no path. A write that fails signals UNSUPPORTED-INPUT naming the path and the
underlying error, so the failure is reported and nothing is swallowed
(SPEC-WORK.md:1294-1325)."
  (let ((path (verification-session-cache-path session)))
    (when (and (stringp path) (plusp (length path)))
      (handler-case
          (write-verification-cache (verification-session-cache session) path)
        (error (c)
          (error 'unsupported-input
                 :what (format nil "verify: cannot write the verification cache ~A: ~A"
                               path c)))))))
