package db

import (
	"github.com/mitghi/btree"
)

const BTREE_DEGREE = 8

var fl = btree.NewFreeList(1024 * 1024 * 128)

type keyTree struct {
	tree *btree.BTree
}

func newKeyTree() *keyTree {
	return &keyTree{
		tree: btree.NewWithFreeList(BTREE_DEGREE, fl, struct{}{}),
	}
}

func (pt *keyTree) Add(ent Key) {
	pt.tree.ReplaceOrInsert(ent)
}

func (pt *keyTree) Has(ent Key) bool {
	return pt.tree.Has(ent)
}

func (pt *keyTree) Len() int {
	return pt.tree.Len()
}

func (pt *keyTree) Cursor() *btree.Cursor {
	return pt.tree.Cursor()
}

func (pt *keyTree) Max() Key {
	max := pt.tree.Max()
	if max == nil {
		return emptyKey
	}
	return max.(Key)
}

func (pt *keyTree) Min() Key {
	min := pt.tree.Min()
	if min == nil {
		return emptyKey
	}
	return min.(Key)
}

func (pt *keyTree) DeleteMax() Key {
	return pt.tree.DeleteMax().(Key)
}

func (pt *keyTree) Delete(k Key) {
	pt.tree.Delete(k)
}

func (pt *keyTree) Iter(iter func(ent Key)) {
	if pt.tree.Len() == 0 {
		return
	}
	max := pt.tree.Max()
	pt.tree.Ascend(func(i btree.Item) bool {
		iter(i.(Key))
		return i != max
	})
}
