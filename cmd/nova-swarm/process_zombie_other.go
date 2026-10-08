//go:build !linux

package main

func processZombie(int) bool { return false }
