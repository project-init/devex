package lookup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestAllAnswersEachDistinctKeyOnce(t *testing.T) {
	var calls atomic.Int32
	results := All(context.Background(), []string{"a", "b", "a", "fail"}, func(_ context.Context, key string) (string, error) {
		calls.Add(1)
		if key == "fail" {
			return "", errors.New("boom")
		}
		return key + "!", nil
	})
	if calls.Load() != 3 {
		t.Errorf("fn ran %d times, want once per distinct key (3)", calls.Load())
	}
	if results["a"].Value != "a!" || results["b"].Value != "b!" || results["fail"].Err == nil {
		t.Errorf("results = %+v, want every answer and the failure kept", results)
	}
}

func TestAllRunsAtMostLimitAtOnce(t *testing.T) {
	var running, peak atomic.Int32
	keys := make([]int, 3*Limit)
	for i := range keys {
		keys[i] = i
	}
	All(context.Background(), keys, func(context.Context, int) (struct{}, error) {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		return struct{}{}, nil
	})
	if got := peak.Load(); got < 2 || got > Limit {
		t.Errorf("peak concurrency = %d, want between 2 and %d", got, Limit)
	}
}
