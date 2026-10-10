// Package tty says whether a file is a terminal and how large its screen is,
// on the systems that have a terminal to ask (the unix family and Windows);
// elsewhere a file is never a terminal and its size is not known.
//
// A size is 0 for what is not known: a file that is not a terminal has none,
// and a terminal may report none. A caller that wants a size treats 0 as
// unknown.
package tty
