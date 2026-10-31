package slos

import (
	"slices"
	"testing"
)

// TestResolvePrefixes reads the app that serves each RPC off the metric names, gives an RPC two apps report a row
// for each, and leaves an RPC no app reports without a prefix.
func TestResolvePrefixes(t *testing.T) {
	eps := []endpoint{
		{Method: "/acme.orders.v1.OrdersService/GetOrder", Metric: "acmeOrdersV1OrdersServiceGetOrder"},
		{Method: "/acme.orders.v1.OrdersService/ListOrders", Metric: "acmeOrdersV1OrdersServiceListOrders"},
		{Method: "/acme.orders.v1.OrdersService/Hidden", Metric: "acmeOrdersV1OrdersServiceHidden"},
	}
	names := []string{
		// Two apps serve GetOrder, as gigapi and login_grpc both serve access RPCs.
		"orders-api.acmeOrdersV1OrdersServiceGetOrder" + p95Suffix,
		"acme.acmeOrdersV1OrdersServiceGetOrder" + p95Suffix,
		"acme.acmeOrdersV1OrdersServiceListOrders" + p95Suffix,
		// Only the p95 metric names the app, and a name with no prefix at all is not one.
		"acme.acmeOrdersV1OrdersServiceHidden.response_time.count",
		"acmeOrdersV1OrdersServiceHidden" + p95Suffix,
	}

	type row struct{ name, prefix, metric string }
	var got []row
	for _, ep := range resolvePrefixes(eps, names) {
		got = append(got, row{ep.Name(), ep.Prefix, ep.MetricName()})
	}
	want := []row{
		{"OrdersService.GetOrder", "acme", "acme.acmeOrdersV1OrdersServiceGetOrder"},
		{"OrdersService.GetOrder", "orders-api", "orders-api.acmeOrdersV1OrdersServiceGetOrder"},
		{"OrdersService.ListOrders", "acme", "acme.acmeOrdersV1OrdersServiceListOrders"},
		{"OrdersService.Hidden", "", ".acmeOrdersV1OrdersServiceHidden"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("resolvePrefixes\n got %+v\nwant %+v", got, want)
	}
}

// TestPinPrefixesAndPrefixesOf puts one prefix on every RPC, and names the apps a report header lists.
func TestPinPrefixesAndPrefixesOf(t *testing.T) {
	eps := []endpoint{{Metric: "a"}, {Metric: "b"}}
	pinned := pinPrefixes(eps, "acme")
	for _, ep := range pinned {
		if ep.Prefix != "acme" {
			t.Errorf("pinPrefixes left %s with prefix %q", ep.Metric, ep.Prefix)
		}
	}
	if eps[0].Prefix != "" {
		t.Error("pinPrefixes changed the endpoints it was given")
	}
	if got := prefixesOf(pinned); !slices.Equal(got, []string{"acme"}) {
		t.Errorf("prefixesOf = %v, want [acme]", got)
	}
	if got := prefixesOf([]endpoint{{Prefix: "b"}, {Prefix: "a"}, {Prefix: "b"}, {}}); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("prefixesOf = %v, want each app once, sorted", got)
	}
}
