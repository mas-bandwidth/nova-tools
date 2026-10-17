// Package ledgerwire is the variable table wire for ledger keys and values,
// cairn log records, fuse boxes and dogfood receipts, generated from
// schemas/ledger/. Every other .go file here is generated: edit the schema and
// run go generate ./pkg/ledgerwire, never the output.
package ledgerwire

//go:generate go run github.com/mas-bandwidth/schema/v2/cmd/schema@v2.5.1-0.20261017012433-9b397a277ad3 generate --lang go --out . ../../schemas/ledger
