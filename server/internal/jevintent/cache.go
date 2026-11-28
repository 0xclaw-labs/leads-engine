// Package jevintent implements a pure-stdlib Hybrid Fast-Path + JEV (/v1/systemone)
// intent classifier and human-handoff router for leads-engine.
//
// Design invariants:
//   - Pure Go stdlib only (CGO_ENABLED=0, zero third-party deps).
//   - Bounded memory: LRU cache capped at 2048 entries / 120s TTL; bounded breaker window.
//   - Exact /v1/systemone wire contract locked to jev-1.14.0 using ONLY "choice" (with
//     criteria map) and "noul" (with criteria {"true","false"}) primitives.
//   - 100% Fail-Safe: any timeout, network error, bad response, or open circuit cleanly
//     falls back to deterministic rule grading.
//   - Secrets (API keys) are never logged or embedded in errors.
package jevintent

import (
	"container/list"
	"sync"
	"time"
)

const (
	DefaultCacheCapacity = 2048
	DefaultCacheTTL      = 120 * time.Second
)

type cacheEntry struct {
	key       string
	value     EvalResult
	expiresAt time.Time
}

// lruCache is a bounded, thread-safe, TTL-enforced LRU cache (stdlib only).
type lruCache struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	nowFn func() time.Time
	ll    *list.List
	items map[string]*list.Element
}

func newLRUCache(capacity int, ttl time.Duration, nowFn func() time.Time) *lruCache {
	if capacity <= 0 {
		capacity = DefaultCacheCapacity
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	return &lruCache{
		cap:   capacity,
		ttl:   ttl,
		nowFn: nowFn,
		ll:    list.New(),
		items: make(map[string]*list.Element, min(capacity, 256)),
	}
}

func (c *lruCache) Get(key string) (EvalResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return EvalResult{}, false
	}
	entry := elem.Value.(*cacheEntry)
	if c.nowFn().After(entry.expiresAt) {
		c.ll.Remove(elem)
		delete(c.items, key)
		return EvalResult{}, false
	}
	c.ll.MoveToFront(elem)
	return entry.value, true
}

func (c *lruCache) Set(key string, val EvalResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp := c.nowFn().Add(c.ttl)
	if elem, ok := c.items[key]; ok {
		c.ll.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.value = val
		entry.expiresAt = exp
		return
	}
	if c.ll.Len() >= c.cap {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheEntry).key)
		}
	}
	elem := c.ll.PushFront(&cacheEntry{
		key:       key,
		value:     val,
		expiresAt: exp,
	})
	c.items[key] = elem
}

func (c *lruCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// singleflight coalesces concurrent identical JEV calls into one upstream RPC.
type sfCall struct {
	wg  sync.WaitGroup
	val EvalResult
	err error
}

type singleflightGroup struct {
	mu    sync.Mutex
	calls map[string]*sfCall
}

func (g *singleflightGroup) Do(key string, fn func() (EvalResult, error)) (EvalResult, error, bool) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*sfCall)
	}
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err, true
	}
	c := &sfCall{}
	c.wg.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		c.wg.Done()
	}()

	c.val, c.err = fn()
	return c.val, c.err, false
}
