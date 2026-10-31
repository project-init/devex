package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigSLOs reads every slos field from its yaml key.
func TestLoadConfigSLOs(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"slos.yaml": `slos:
  module: github.com/acme/protos
  version: v1.2.3
  file: sre/slos.gen.yaml
  group: orders
  metricPrefix: acme
  datadog:
    site: datadoghq.eu
    scope: env:production
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := SLOsConfiguration{
		Module: "github.com/acme/protos", Version: "v1.2.3", File: "sre/slos.gen.yaml", Group: "orders", MetricPrefix: "acme",
		Datadog: SLOsDatadogConfiguration{Site: "datadoghq.eu", Scope: "env:production"},
	}
	if cfg.SLOs != want {
		t.Errorf("SLOs config\n got %+v\nwant %+v", cfg.SLOs, want)
	}
}
