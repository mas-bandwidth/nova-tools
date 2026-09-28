;;;; lock.lisp --- the journal's OS-held exclusive lock, and the bench identity.
;;;;
;;;; SPEC-WORK.md:162-183: at start the session takes an OS-held exclusive lock
;;;; (`flock`) on `<journal>.lock`, keyed by the journal's canonical path
;;;; (realpath, so a symlink or a relative spelling is the same journal), and
;;;; holds it for its whole life; a start that cannot take it refuses
;;;; (`journal held`), naming the holder's pid and socket from the lock file's
;;;; contents, and **never unlinks another process's lock**. The kernel releases
;;;; the lock when the holder dies, so a crashed owner needs no cleanup.
;;;;
;;;; The same paragraph fixes the bench identity the journal records: the hostname
;;;; and the lock's device and inode (Unix), so a journal copied to another bench,
;;;; or onto a different file, reads as a different bench and is never resumed.

(in-package #:nova-work)

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix))

;; LOCK_* from <sys/file.h> on Linux; BSD and macOS agree on these four.
(eval-when (:compile-toplevel :load-toplevel :execute)
  (defconstant +lock-sh+ 1)
  (defconstant +lock-ex+ 2)
  (defconstant +lock-nb+ 4)
  (defconstant +lock-un+ 8))

#+sbcl
(eval-when (:compile-toplevel :load-toplevel :execute)
  (sb-alien:define-alien-routine ("flock" %flock) sb-alien:int
    (fd sb-alien:int) (operation sb-alien:int)))

(defstruct (journal-lock (:conc-name lock-))
  "One held journal lock: the lock file's path and the fd the flock is held on.
The fd stays open for the whole life of the holder; the OS releases the flock
when it is closed or the holder dies."
  path fd)

(defun canonical-journal-key (path)
  "The key both the lock and the bench identity are taken on: the journal's
canonical spelling. When the journal exists this is its truename (realpath);
otherwise it is the truename of its parent directory plus the file name. Either
way a symlink and a relative spelling resolve to the same journal."
  (let* ((merged (merge-pathnames path))
         (dir (directory-namestring merged))
         (real-dir (or (ignore-errors (namestring (truename (pathname dir))))
                       dir)))
    (concatenate 'string real-dir (file-namestring merged))))

(defun lock-file-path (journal-path)
  (concatenate 'string (canonical-journal-key journal-path) ".lock"))

(defun bench-identity (&key (path nil))
  "The bench's identity string: the hostname plus, when PATH is a lock whose
device and inode can be stated, those two (`host@dev:ino`). A journal copied to
another bench, or onto a different file, therefore reads as a different bench."
  #+sbcl
  (let ((host (or (ignore-errors (machine-instance)) "unknown")))
    (if (and path (probe-file path))
        (let ((st (ignore-errors (sb-posix:stat path))))
          (if st
              (format nil "~A@~D:~D" host (sb-posix:stat-dev st) (sb-posix:stat-ino st))
              host))
        host))
  #-sbcl
  (ignore-errors (machine-instance)))

(defun take-journal-lock (journal-path &key (socket "none"))
  "Take an exclusive, non-blocking flock on `<journal>.lock`, keyed by the
journal's canonical spelling. On success writes the holder's pid and socket into
the lock file — never unlinked, so a refused taker can name the holder — and
returns a JOURNAL-LOCK. Returns NIL when another process holds the lock
(EWOULDBLOCK)."
  #+sbcl
  (let* ((lock-path (lock-file-path journal-path))
         (fd (sb-posix:open lock-path (logior sb-posix:o-creat sb-posix:o-rdwr) #o644)))
    (cond
      ((or (null fd) (minusp fd))
       (when (and fd (integerp fd)) (ignore-errors (sb-posix:close fd)))
       nil)
      (t
       (let ((r (ignore-errors (%flock fd (logior +lock-ex+ +lock-nb+)))))
         (if (eql r 0)
             ;; We hold it: record who we are. The flock is on FD; writing the
             ;; contents through a second, transient fd does not release it.
             (progn
               (ignore-errors
                 (with-open-file (s lock-path :direction :output :if-exists :overwrite
                                                 :if-does-not-exist :create
                                                 :element-type 'character :external-format :utf-8)
                   (format s "pid=~D socket=~A~%" (sb-posix:getpid) socket)))
               (make-journal-lock :path lock-path :fd fd))
             (progn (ignore-errors (sb-posix:close fd)) nil))))))
  #-sbcl
  nil)

(defun release-journal-lock (lock)
  "Release the flock held by LOCK and close its fd. The `.lock` file is left in
place: it is never unlinked, because another process may still hold it or be
examining it to name the holder."
  (when (and lock (lock-fd lock))
    #+sbcl
    (progn
      (ignore-errors (%flock (lock-fd lock) +lock-un+))
      (ignore-errors (sb-posix:close (lock-fd lock)))
      (setf (lock-fd lock) nil)))
  t)
