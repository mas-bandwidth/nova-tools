// Package sprintwire is how a worker talks to the sprint's server. The sprint
// answer records (ResultWire and ResponseWire) are tables generated from
// sprint.schema by the pinned schema compiler; every other .go file generated
// here is generated output, never edited by hand. pkg/schemaread reads the
// response strictly: the binary wire, the padded standard base64 of the wire,
// the schema JSON text, and the legacy JSON the server writes today, each
// refused by name when damaged.
package sprintwire

//go:generate go run github.com/mas-bandwidth/schema/v2/cmd/schema@v2.5.1-0.20261017012433-9b397a277ad3 generate --lang go --out . ./sprint.schema
