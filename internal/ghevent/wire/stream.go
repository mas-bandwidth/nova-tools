// Package wire holds the stream identity shared by GitHub event writers and
// readers. It has no imports: a consumer does not need webhook decoding or
// any particular Redis client to name the stream.
package wire

// Stream is the existing Redis key for carried GitHub deliveries. Changing
// it requires coordinating every producer and reader and migrating cursors;
// extracting the constant does not rename the stream or change its fields.
const Stream = "ev:github"
