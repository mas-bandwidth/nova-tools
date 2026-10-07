package main

const bech32Alphabet = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// agePub is a key age's own alphabet makes valid, so the fixture needs no age-keygen.
func agePub(lead byte) string {
	return "age1" + string(lead) + (bech32Alphabet + bech32Alphabet[:26])[1:]
}
