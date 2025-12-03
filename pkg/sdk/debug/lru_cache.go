package debug

import (
	"container/list"
	"sync"
)

// lruCache is a simple LRU (Least Recently Used) cache with a fixed capacity.
type lruCache struct {
	capacity int
	mu       sync.RWMutex
	items    map[string]*list.Element
	lruList  *list.List
}

// lruEntry represents a key-value pair in the cache.
type lruEntry struct {
	key   string
	value *FunctionMetadata
}

// newLRUCache creates a new LRU cache with the specified capacity.
func newLRUCache(capacity int) *lruCache {
	return &lruCache{
		capacity: capacity,
		items:    make(map[string]*list.Element),
		lruList:  list.New(),
	}
}

// Get retrieves a value from the cache and marks it as recently used.
func (c *lruCache) Get(key string) (*FunctionMetadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		// Move to front (most recently used).
		c.lruList.MoveToFront(elem)
		return elem.Value.(*lruEntry).value, true
	}

	return nil, false
}

// Put adds or updates a value in the cache.
func (c *lruCache) Put(key string, value *FunctionMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If key exists, update and move to front.
	if elem, ok := c.items[key]; ok {
		c.lruList.MoveToFront(elem)
		elem.Value.(*lruEntry).value = value
		return
	}

	// Add new entry.
	entry := &lruEntry{key: key, value: value}
	elem := c.lruList.PushFront(entry)
	c.items[key] = elem

	// Evict least recently used if over capacity.
	if c.lruList.Len() > c.capacity {
		c.evictOldest()
	}
}

// evictOldest removes the least recently used item from the cache.
func (c *lruCache) evictOldest() {
	elem := c.lruList.Back()
	if elem != nil {
		c.lruList.Remove(elem)
		entry := elem.Value.(*lruEntry)
		delete(c.items, entry.key)
	}
}

// Len returns the current number of items in the cache.
func (c *lruCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lruList.Len()
}
