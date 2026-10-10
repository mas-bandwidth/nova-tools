//go:build !darwin && !linux

package main

import "github.com/mas-bandwidth/nova-tools/pkg/hostload"

func localBox() Box { return Box{Load1: hostload.Local().Load1} }
