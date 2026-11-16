package jira

import (
	"sync"
	"testing"

	"github.com/project-init/gommon/pkg/jiraclient"
)

// Run under -race: apply executes Jira operations side by side, and every one may touch the cache.
func TestLinkCacheTakesConcurrentUse(t *testing.T) {
	var cache linkCache
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			cache.store("DEVEX-1", []jiraclient.IssueLink{{Type: "Blocks", InwardIssue: "DEVEX-2"}})
			cache.add("DEVEX-1", "Blocks", "DEVEX-3")
			cache.lookup("DEVEX-1", "Blocks", "DEVEX-2")
			cache.forget("DEVEX-1")
		})
	}
	wg.Wait()
	cache.store("DEVEX-1", []jiraclient.IssueLink{{Type: "Blocks", InwardIssue: "DEVEX-2"}})
	if found, cached := cache.lookup("DEVEX-1", "Blocks", "DEVEX-2"); !found || !cached {
		t.Errorf("lookup() = %v, %v; want the stored link", found, cached)
	}
	cache.forget("DEVEX-1")
	if _, cached := cache.lookup("DEVEX-1", "Blocks", "DEVEX-2"); cached {
		t.Error("lookup() after forget still cached")
	}
}

func TestStoreKeepsALinkAddedSinceTheRead(t *testing.T) {
	var cache linkCache
	cache.add("DEVEX-1", "Blocks", "DEVEX-3")                                                // a worker linked it
	cache.store("DEVEX-1", []jiraclient.IssueLink{{Type: "Blocks", InwardIssue: "DEVEX-2"}}) // a snapshot read earlier
	if found, _ := cache.lookup("DEVEX-1", "Blocks", "DEVEX-3"); !found {
		t.Error("store dropped a link added after its snapshot was read")
	}
}
