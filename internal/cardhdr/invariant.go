package cardhdr

// KeyPaths is the card header's PATHS key, the one spelling of it: a card's
// PATHS line names the files and packages the work spans, and internal/cardtree
// and internal/decide read that line through this key, so a reader of a card
// header reads it the same way everywhere.
const KeyPaths = "PATHS"
