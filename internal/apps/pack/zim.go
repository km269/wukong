// Package pack provides application packaging functionality.
//
// ZIM archive support delegates to the public pkg/zim package.
// This file provides backward-compatible wrappers for internal
// callers that already reference the pack.ZIMPacker type.
package pack

import (
	"github.com/km269/wukong/pkg/zim"
)

// ZIMPacker is a backward-compatible alias for zim.Packer.
// New code should use zim.Packer directly.
type ZIMPacker = zim.Packer

// NewZIMPacker creates a new ZIM packer via the pkg/zim package.
func NewZIMPacker() *ZIMPacker {
	return zim.NewPacker()
}
