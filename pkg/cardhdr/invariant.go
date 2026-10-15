package cardhdr

// KeyKind is the card header's KIND key, the one spelling of it.
const KeyKind = "KIND"

// KeyPaths is the card header's PATHS key, the one spelling of it: a card's
// PATHS line names the files and packages the work spans, and pkg/cardtree
// and pkg/decide read that line through this key, so a reader of a card
// header reads it the same way everywhere.
const KeyPaths = "PATHS"

// KeyRepo is the card header's REPO key, the one spelling of it.
const KeyRepo = "REPO"
