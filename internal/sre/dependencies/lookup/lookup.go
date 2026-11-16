// Package lookup runs independent registry, network, and command lookups concurrently.
package lookup

import (
	"context"
	"sync"
)

// Limit bounds the lookups one All call runs at once, so a large repository neither floods a
// registry nor forks a process per key. Planning runs a call per kind of lookup side by side.
const Limit = 8

// Result is one key's answer.
type Result[V any] struct {
	Value V
	Err   error
}

// All calls fn once for each distinct key, at most Limit at a time, and returns every answer by
// key. A failed call stops none of the others, so callers can report failures in their own
// order rather than in the order they happened.
func All[K comparable, V any](ctx context.Context, keys []K, fn func(context.Context, K) (V, error)) map[K]Result[V] {
	results := make(map[K]Result[V], len(keys))
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, Limit)
	started := make(map[K]bool, len(keys))
	for _, key := range keys {
		if started[key] {
			continue
		}
		started[key] = true
		wg.Go(func() {
			slots <- struct{}{}
			value, err := fn(ctx, key)
			<-slots
			mu.Lock()
			results[key] = Result[V]{Value: value, Err: err}
			mu.Unlock()
		})
	}
	wg.Wait()
	return results
}
