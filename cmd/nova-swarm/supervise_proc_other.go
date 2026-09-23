//go:build !unix

package main

func superviseTerm() (<-chan struct{}, func()) {
	return nil, func() {}
}
