package abi

import (
	"fmt"
	"runtime"
	"strings"
)

const (
	Bits32 = 32
	Bits64 = 64
)

var sizeBits = DefaultSizeBitsForArch(runtime.GOARCH)

func DefaultSizeBitsForArch(arch string) int {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "386", "arm", "mips", "mipsle", "wasm":
		return Bits32
	default:
		return Bits64
	}
}

func SizeBits() int {
	return sizeBits
}

func SetSizeBits(bits int) error {
	if bits == 0 {
		bits = DefaultSizeBitsForArch(runtime.GOARCH)
	}
	switch bits {
	case Bits32, Bits64:
		sizeBits = bits
		return nil
	default:
		return fmt.Errorf("invalid target ABI size %d (expected 32 or 64)", bits)
	}
}

func PointerBytes() int64 {
	return int64(SizeBits() / 8)
}
