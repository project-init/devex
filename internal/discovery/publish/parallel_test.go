package publish

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

// independentCreates plans n creates that depend on nothing.
func independentCreates(n int) []provider.Operation {
	operations := make([]provider.Operation, n)
	for i := range operations {
		id := fmt.Sprintf("WI-%03d", i+1)
		operations[i] = provider.Operation{ID: "create-" + id, Action: provider.ActionCreateIssue, ItemID: domain.ItemID(id), IdempotencyKey: id}
	}
	return operations
}

func TestApplyRunsIndependentOperationsSideBySide(t *testing.T) {
	timed := func(adapter provider.Adapter) time.Duration {
		planPath := writePlan(t, newBundle(t), independentCreates(20)...)
		start := time.Now()
		if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	parallel := parallelAdapter{&fakeAdapter{executions: map[string]int{}, delay: 20 * time.Millisecond}}
	sequential := timed(&fakeAdapter{executions: map[string]int{}, delay: 20 * time.Millisecond})
	concurrent := timed(parallel)
	if parallel.peak != parallel.MaxConcurrent() || concurrent > sequential*6/10 {
		t.Errorf("peak = %d, %s against %s sequential; want %d at once and well under the sequential time",
			parallel.peak, concurrent, sequential, parallel.MaxConcurrent())
	}
	t.Logf("20 creates at 20ms each: %s sequential, %s with %d at once", sequential, concurrent, parallel.peak)
}

func TestApplyWaitsForDependencies(t *testing.T) {
	operations := independentCreates(3)
	operations[2].DependsOn = []string{operations[0].ID, operations[1].ID}
	planPath := writePlan(t, newBundle(t), operations...)
	adapter := parallelAdapter{&fakeAdapter{executions: map[string]int{}, resolvedAt: map[string][]domain.ItemID{}}}
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err != nil {
		t.Fatal(err)
	}
	// Execute saw both dependencies' remote work, so they had finished.
	if got := adapter.resolvedAt[operations[2].ID]; len(got) != 2 || got[0] != operations[0].ItemID || got[1] != operations[1].ItemID {
		t.Errorf("%s saw %v resolved, want both dependencies", operations[2].ID, got)
	}
}

// parallelAdapter is a fake that, like Jira, takes four operations at once.
type parallelAdapter struct{ *fakeAdapter }

func (parallelAdapter) MaxConcurrent() int { return 4 }

func TestApplyRunsOneAtATimeUnlessTheAdapterSaysOtherwise(t *testing.T) {
	planPath := writePlan(t, newBundle(t), independentCreates(4)...)
	adapter := &fakeAdapter{executions: map[string]int{}, delay: 5 * time.Millisecond}
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err != nil {
		t.Fatal(err)
	}
	if adapter.peak != 1 {
		t.Errorf("peak = %d, want 1 for an adapter that declares no limit", adapter.peak)
	}
}

func TestApplyStartsNothingAfterAFailure(t *testing.T) {
	operations := independentCreates(4)
	planPath := writePlan(t, newBundle(t), operations...)
	adapter := &fakeAdapter{executions: map[string]int{}, failOnce: operations[1].ID}
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err == nil {
		t.Fatal("Apply() succeeded, want the planned failure")
	}
	if adapter.executions[operations[2].ID] != 0 || adapter.executions[operations[3].ID] != 0 {
		t.Errorf("executions = %v, want nothing started after the failure", adapter.executions)
	}
}

func TestInterruptedParallelApplyResumesWithoutDuplicates(t *testing.T) {
	operations := independentCreates(20)
	operations[19].DependsOn = []string{operations[9].ID}
	planPath := writePlan(t, newBundle(t), operations...)
	adapter := parallelAdapter{&fakeAdapter{executions: map[string]int{}, delay: 5 * time.Millisecond, failOnce: operations[9].ID}}
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err == nil {
		t.Fatal("first Apply() succeeded, want the planned failure")
	}
	receipt, err := Apply(context.Background(), planPath, adapter, ApplyHooks{})
	if err != nil || receipt.Status != "complete" {
		t.Fatalf("resumed Apply() = %v, status %q", err, receipt.Status)
	}
	for _, operation := range operations {
		want := 1
		if operation.ID == operations[9].ID {
			want = 2 // the failed attempt, then the retry
		}
		if got := adapter.executions[operation.ID]; got != want {
			t.Errorf("%s ran %d times, want %d", operation.ID, got, want)
		}
	}
}

func TestApplyReportsADependencyCycle(t *testing.T) {
	operations := independentCreates(2)
	operations[0].DependsOn = []string{operations[1].ID}
	operations[1].DependsOn = []string{operations[0].ID}
	planPath := writePlan(t, newBundle(t), operations...)
	adapter := &fakeAdapter{executions: map[string]int{}}
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err == nil || !strings.Contains(err.Error(), "cycle") || len(adapter.executions) != 0 {
		t.Errorf("Apply() = %v after %d executions, want the cycle reported and nothing run", err, len(adapter.executions))
	}
}
