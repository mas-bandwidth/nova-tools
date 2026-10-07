// Package typedrec is the one typed parser: the home of every typed line a
// nova tool reads, the RESULT v2 record and DISPOSITION line first, and the
// Lua replies (the PATHS gate's refusal) and any other typed line with
// them. No other package compares a line's token to a bare word, builds a
// table of the tokens, or cuts a token's prefix: the class test
// TestOneTypedParser walks the tree for those shapes and skips this package
// because of that, not by accident.
package typedrec
