package main

import "flag"

// observeVerbFlags lets the documentation contract test inspect the exact
// FlagSet each public verb accepts.
var observeVerbFlags = func(string, *flag.FlagSet) {}
