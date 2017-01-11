//go:generate msgp
package db

import (
	"encoding/binary"

	"github.com/google/btree"
)

type Key [4]byte

func (k Key) Less(than btree.Item) bool {
	t := than.(Key)
	return binary.LittleEndian.Uint32(k[:]) < binary.LittleEndian.Uint32(t[:])
}

func (k *Key) FromSlice(src []byte) {
	copy(k[:], src)
}

func (k Key) String() string {
	return string(k[:])
}
