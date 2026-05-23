;;; Candidate GNU Guix package definition for upstream submission.
;;;
;;; Before submitting to Guix, replace the source hash and add Guix package
;;; inputs for any Go modules that are not already available in Guix.

(define-module (clambhook)
  #:use-module ((guix licenses) #:prefix license:)
  #:use-module (guix build-system gnu)
  #:use-module (guix download)
  #:use-module (guix packages)
  #:use-module (gnu packages glib)
  #:use-module (gnu packages golang)
  #:use-module (gnu packages gnome)
  #:use-module (gnu packages gtk)
  #:use-module (gnu packages meson)
  #:use-module (gnu packages ninja)
  #:use-module (gnu packages pkg-config)
  #:use-module (gnu packages sodium))

(define-public clambhook
  (package
    (name "clambhook")
    (version "0.1.0")
    (source
     (origin
       (method url-fetch)
       (uri (string-append
             "https://github.com/JohnThre/clambhook/archive/refs/tags/v"
             version ".tar.gz"))
       (sha256
        (base32 "0000000000000000000000000000000000000000000000000000"))))
    (build-system gnu-build-system)
    (arguments
     (list
      #:tests? #t
      #:phases
      #~(modify-phases %standard-phases
          (delete 'configure)
          (replace 'build
            (lambda* (#:key parallel-build? #:allow-other-keys)
              (invoke "make" "build"
                      (string-append "-j"
                                     (if parallel-build?
                                         (number->string (parallel-job-count))
                                         "1"))
                      (string-append "VERSION=" #$version))
              (invoke "make" "build-linux"
                      (string-append "VERSION=" #$version))))
          (replace 'check
            (lambda _
              (invoke "make" "test")))
          (replace 'install
            (lambda _
              (invoke "make" "install"
                      (string-append "DESTDIR=" #$output)
                      "PREFIX=")
              (invoke "make" "install-linux"
                      (string-append "DESTDIR=" #$output)
                      "PREFIX="))))))
    (native-inputs
     (list go meson ninja pkg-config vala))
    (inputs
     (list gtk libadwaita libgee glib json-glib libsecret libsodium libsoup))
    (home-page "https://github.com/JohnThre/clambhook")
    (synopsis "Local network client daemon, desktop controller, and terminal dashboard")
    (description
     "clambhook provides a local network client daemon, HTTP control API, and
native GTK desktop controller plus terminal dashboard.")
    (license license:gpl3)))

clambhook
