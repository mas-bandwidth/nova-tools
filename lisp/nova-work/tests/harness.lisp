;;;; harness.lisp --- the smallest runner that prints one summary line.

(in-package #:nova-work/tests)

(defvar *tests* '())
(defvar *pass* 0)
(defvar *fail* 0)
(defvar *current* nil)
(defvar *problems* '())

(defmacro deftest (name spec-line expected &body body)
  `(push (list ,name ,spec-line ,expected (lambda () ,@body)) *tests*))

(define-condition check-failed (error)
  ((detail :initarg :detail :reader check-failed-detail))
  (:report (lambda (c s) (write-string (check-failed-detail c) s))))

(defun fail (fmt &rest args)
  (error 'check-failed :detail (apply #'format nil fmt args)))

(defun ok (test fmt &rest args)
  (unless test (apply #'fail fmt args))
  t)

(defun check-equal (expected actual what)
  (unless (equal expected actual)
    (fail "~A: expected ~S got ~S" what expected actual))
  t)

(defun check-string= (expected actual what)
  (unless (and (stringp actual) (string= expected actual))
    (fail "~A: expected ~S got ~S" what expected actual))
  t)

(defun run-all ()
  (setf *pass* 0 *fail* 0 *problems* '())
  (dolist (entry (reverse *tests*))
    (destructuring-bind (name spec expected thunk) entry
      (let ((*current* name))
        (handler-case
            (progn (funcall thunk)
                   (incf *pass*)
                   (format t "TEST ~A PASS spec=~A ~A~%" name spec expected))
          (error (c)
            (incf *fail*)
            (push (list name spec c) *problems*)
            (format t "TEST ~A FAIL spec=~A ~A: ~A~%" name spec expected c))))))
  (format t "NOVA-WORK SLICE1 total=~D pass=~D fail=~D~%"
          (+ *pass* *fail*) *pass* *fail*)
  (finish-output)
  (if (zerop *fail*) 0 1))

(defun main ()
  (let ((code (run-all)))
    #+sbcl (sb-ext:exit :code code :abort nil)
    #-sbcl (progn code)))
