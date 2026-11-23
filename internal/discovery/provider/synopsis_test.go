package provider

import "testing"

// A bundle without a synopsis must plan the description it always has, or sync would rewrite
// every published initiative.
func TestWithSynopsis(t *testing.T) {
	if got := WithSynopsis("", "Deliver audit logs."); got != "Deliver audit logs." {
		t.Fatalf("WithSynopsis(\"\", ...) = %q, want the description unchanged", got)
	}
	if got := WithSynopsis("Synopsis.", "Deliver audit logs."); got != "Synopsis.\n\nDeliver audit logs." {
		t.Fatalf("WithSynopsis() = %q", got)
	}
}
