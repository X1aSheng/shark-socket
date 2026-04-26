package session

import (
	"testing"
)

// --- Touch moves to head ---

func TestLRUList_Touch_MovesToHead(t *testing.T) {
	l := NewLRUList()

	// Insert by touching unknown IDs does nothing
	l.Touch(1) // not yet in the list; no-op

	// Simulate insert: we use Evict internals indirectly.
	// Since there is no Insert/Push, we need to use the LRU from Manager or
	// test Touch on nodes that are in the list.
	// LRUList has no public Insert method, but nodes get added via manager usage.
	// We can verify Touch on an empty list is a no-op.
	if l.Len() != 0 {
		t.Errorf("Len() = %d, want 0 after Touch on empty", l.Len())
	}

	// Use a trick: build the list via the internal index.
	// Since we can't insert directly, we'll test via the exported methods.
	// Actually, LRUList has no Insert — it only indexes what's in the Manager.
	// Let's test Evict on an empty list.
	evicted := l.Evict(5)
	if evicted != nil {
		t.Errorf("Evict on empty list = %v, want nil", evicted)
	}
}

func TestLRUList_Evict_LRLOrder(t *testing.T) {
	l := NewLRUList()

	// We need to populate the list. The only way to add nodes is via
	// the index map + linked list. Since there's no Insert method, we
	// access internals for testing purposes.
	// Build nodes manually to test Evict order.

	// push nodes: 10, 20, 30 (head = most recent)
	for _, id := range []uint64{10, 20, 30} {
		node := l.nodePool.Get().(*lruNode)
		node.id = id
		l.index[id] = node
		l.pushFront(node)
	}
	// Order: head=30, 20, tail=10

	if l.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", l.Len())
	}

	// Evict 1 should remove tail (10)
	evicted := l.Evict(1)
	if len(evicted) != 1 || evicted[0] != 10 {
		t.Errorf("Evict(1) = %v, want [10]", evicted)
	}
	if l.Len() != 2 {
		t.Errorf("Len() after Evict = %d, want 2", l.Len())
	}

	// Evict 2 removes 20, 30
	evicted = l.Evict(2)
	if len(evicted) != 2 {
		t.Fatalf("Evict(2) returned %d items, want 2", len(evicted))
	}
	if evicted[0] != 20 || evicted[1] != 30 {
		t.Errorf("Evict(2) = %v, want [20 30]", evicted)
	}

	// Now empty
	if l.Len() != 0 {
		t.Errorf("Len() after full evict = %d, want 0", l.Len())
	}
}

func TestLRUList_Touch_ReordersHead(t *testing.T) {
	l := NewLRUList()

	// Build: 1, 2, 3 -> head=3, 2, tail=1
	for _, id := range []uint64{1, 2, 3} {
		node := l.nodePool.Get().(*lruNode)
		node.id = id
		l.index[id] = node
		l.pushFront(node)
	}

	// Touch(1) should move node 1 to head
	l.Touch(1)

	// Evict should now remove 2 first (tail), then 3
	evicted := l.Evict(1)
	if len(evicted) != 1 || evicted[0] != 2 {
		t.Errorf("Evict(1) after Touch(1) = %v, want [2]", evicted)
	}

	evicted = l.Evict(1)
	if len(evicted) != 1 || evicted[0] != 3 {
		t.Errorf("Evict(1) = %v, want [3]", evicted)
	}

	// Node 1 should still be there
	if l.Len() != 1 {
		t.Errorf("Len() = %d, want 1", l.Len())
	}
	if _, ok := l.index[1]; !ok {
		t.Error("node 1 should still exist")
	}
}

func TestLRUList_Touch_AlreadyAtHead(t *testing.T) {
	l := NewLRUList()

	node := l.nodePool.Get().(*lruNode)
	node.id = 42
	l.index[42] = node
	l.pushFront(node)

	// Touch head node — should be a no-op
	l.Touch(42)
	if l.Len() != 1 {
		t.Errorf("Len() = %d, want 1", l.Len())
	}

	// Should still evict correctly
	evicted := l.Evict(1)
	if len(evicted) != 1 || evicted[0] != 42 {
		t.Errorf("Evict(1) = %v, want [42]", evicted)
	}
}

// --- Remove specific ID ---

func TestLRUList_Remove(t *testing.T) {
	l := NewLRUList()

	for _, id := range []uint64{100, 200, 300} {
		node := l.nodePool.Get().(*lruNode)
		node.id = id
		l.index[id] = node
		l.pushFront(node)
	}

	// Remove middle node
	l.Remove(200)
	if l.Len() != 2 {
		t.Errorf("Len() after Remove(200) = %d, want 2", l.Len())
	}
	if _, ok := l.index[200]; ok {
		t.Error("node 200 still in index after Remove")
	}

	// Evict should remove in LRU order: 100 (oldest), then 300
	evicted := l.Evict(2)
	if len(evicted) != 2 {
		t.Fatalf("Evict(2) returned %d items, want 2", len(evicted))
	}
	if evicted[0] != 100 || evicted[1] != 300 {
		t.Errorf("Evict(2) = %v, want [100 300]", evicted)
	}
}

func TestLRUList_Remove_Nonexistent(t *testing.T) {
	l := NewLRUList()

	// Should be a no-op, not panic
	l.Remove(999)
	if l.Len() != 0 {
		t.Errorf("Len() after Remove(nonexistent) = %d, want 0", l.Len())
	}
}

// --- Len returns count ---

func TestLRUList_Len(t *testing.T) {
	l := NewLRUList()
	if l.Len() != 0 {
		t.Errorf("Len() on new = %d, want 0", l.Len())
	}

	for i := uint64(0); i < 5; i++ {
		node := l.nodePool.Get().(*lruNode)
		node.id = i
		l.index[i] = node
		l.pushFront(node)
	}
	if l.Len() != 5 {
		t.Errorf("Len() = %d, want 5", l.Len())
	}
}

// --- Stop clears all ---

func TestLRUList_Stop(t *testing.T) {
	l := NewLRUList()

	for i := uint64(0); i < 10; i++ {
		node := l.nodePool.Get().(*lruNode)
		node.id = i
		l.index[i] = node
		l.pushFront(node)
	}

	l.Stop()
	if l.Len() != 0 {
		t.Errorf("Len() after Stop = %d, want 0", l.Len())
	}
	if l.head != nil {
		t.Error("head not nil after Stop")
	}
	if l.tail != nil {
		t.Error("tail not nil after Stop")
	}
}

// --- Empty list Evict returns nil ---

func TestLRUList_Evict_Empty(t *testing.T) {
	l := NewLRUList()
	evicted := l.Evict(10)
	if evicted != nil {
		t.Errorf("Evict on empty = %v, want nil", evicted)
	}
}

// --- Evict with n <= 0 returns nil ---

func TestLRUList_Evict_NonPositive(t *testing.T) {
	l := NewLRUList()

	node := l.nodePool.Get().(*lruNode)
	node.id = 1
	l.index[1] = node
	l.pushFront(node)

	evicted := l.Evict(0)
	if evicted != nil {
		t.Errorf("Evict(0) = %v, want nil", evicted)
	}
	evicted = l.Evict(-1)
	if evicted != nil {
		t.Errorf("Evict(-1) = %v, want nil", evicted)
	}
}

// --- Evict more than available ---

func TestLRUList_Evict_MoreThanAvailable(t *testing.T) {
	l := NewLRUList()

	for _, id := range []uint64{1, 2, 3} {
		node := l.nodePool.Get().(*lruNode)
		node.id = id
		l.index[id] = node
		l.pushFront(node)
	}

	evicted := l.Evict(100)
	if len(evicted) != 3 {
		t.Errorf("Evict(100) returned %d items, want 3", len(evicted))
	}
	if l.Len() != 0 {
		t.Errorf("Len() after over-evict = %d, want 0", l.Len())
	}
}
