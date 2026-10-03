// version is empty in an ordinary build and is the release stamp: -ldflags
// "-X main.version=<tag>" writes it, which is why it is a string var. The
// skeleton's version verb prints it through internal/buildinfo, one line for
// every binary (docs/STANDARD.md section 2).
package main

var version string
