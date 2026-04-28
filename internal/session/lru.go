package session

import "sync"

type lruNode struct {
	prev *lruNode
	next *lruNode
	id   uint64
}

// LRUList is a doubly-linked list with O(1) Touch/Remove and O(n) Evict.
// All methods are NOT thread-safe; callers must hold the shard lock.
type LRUList struct {
	head     *lruNode
	tail     *lruNode
	index    map[uint64]*lruNode
	nodePool sync.Pool
}

// NewLRUList creates a new LRU list.
func NewLRUList() *LRUList {
	return &LRUList{
		index: make(map[uint64]*lruNode),
		nodePool: sync.Pool{
			New: func() any { return &lruNode{} },
		},
	}
}

// Touch moves the node for id to the head (most recently used).
// If id is not in the list, it is inserted at the head.
func (l *LRUList) Touch(id uint64) {
	node, ok := l.index[id]
	if !ok {
		node = l.nodePool.Get().(*lruNode)
		node.id = id
		l.index[id] = node
		l.pushFront(node)
		return
	}
	if node == l.head {
		return // already at head
	}
	l.detach(node)
	l.pushFront(node)
}

// Evict removes and returns the n least recently used IDs from the tail.
func (l *LRUList) Evict(n int) []uint64 {
	if n <= 0 || l.tail == nil {
		return nil
	}
	evicted := make([]uint64, 0, n)
	for i := 0; i < n && l.tail != nil; i++ {
		node := l.tail
		l.detach(node)
		delete(l.index, node.id)
		evicted = append(evicted, node.id)
		node.prev = nil
		node.next = nil
		l.nodePool.Put(node)
	}
	return evicted
}

// Remove removes the node for the given id.
func (l *LRUList) Remove(id uint64) {
	node, ok := l.index[id]
	if !ok {
		return
	}
	l.detach(node)
	delete(l.index, id)
	node.prev = nil
	node.next = nil
	l.nodePool.Put(node)
}

// Len returns the number of items in the list.
func (l *LRUList) Len() int {
	return len(l.index)
}

// Stop clears the list.
func (l *LRUList) Stop() {
	l.head = nil
	l.tail = nil
	l.index = make(map[uint64]*lruNode)
}

func (l *LRUList) detach(node *lruNode) {
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		l.head = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		l.tail = node.prev
	}
	node.prev = nil
	node.next = nil
}

func (l *LRUList) pushFront(node *lruNode) {
	node.next = l.head
	node.prev = nil
	if l.head != nil {
		l.head.prev = node
	}
	l.head = node
	if l.tail == nil {
		l.tail = node
	}
}
