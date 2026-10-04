//go:build !darwin && !linux

package local

import "github.com/mas-bandwidth/nova-tools/internal/hostload"

func localBox() Box { return Box{Load1: hostload.Local().Load1} }
