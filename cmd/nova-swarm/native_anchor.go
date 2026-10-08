package main

import (
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
)

const nativeAnchorReceiptName = ".native-anchor"

func startNativeAnchor(slot string, pgid int) error {
	return procgroup.StartAnchor(pgid, filepath.Join(slot, nativeAnchorReceiptName))
}

func anchorInGroup(slot string, pgid int) bool {
	return procgroup.AnchorPinned(pgid, filepath.Join(slot, nativeAnchorReceiptName))
}
