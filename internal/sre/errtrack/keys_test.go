package errtrack

import (
	"strings"
	"testing"
)

// TestLoadKeys checks the messages, which are the whole value of this function:
// a half-configured environment is a typo, and naming the wrong variable sends
// someone inspecting the one that is already set.
func TestLoadKeys(t *testing.T) {
	tests := map[string]struct {
		env         map[string]string
		wantNamed   []string
		wantUnnamed []string
	}{
		"both set": {
			env: map[string]string{apiKeyEnv: "api", appKeyEnv: "app"},
		},
		"no app key": {
			env:       map[string]string{apiKeyEnv: "api"},
			wantNamed: []string{appKeyEnv}, wantUnnamed: []string{apiKeyEnv},
		},
		// An exported but empty variable has configured nothing.
		"app key exported empty": {
			env:       map[string]string{apiKeyEnv: "api", appKeyEnv: ""},
			wantNamed: []string{appKeyEnv}, wantUnnamed: []string{apiKeyEnv},
		},
		"neither": {
			wantNamed: []string{apiKeyEnv, appKeyEnv},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			apiKey, appKey, err := loadKeys(func(k string) string { return tt.env[k] })

			if len(tt.wantNamed) == 0 {
				if err != nil {
					t.Fatalf("loadKeys() error = %v", err)
				}
				if apiKey != "api" || appKey != "app" {
					t.Errorf("loadKeys() = %q, %q", apiKey, appKey)
				}

				return
			}

			if err == nil {
				t.Fatalf("loadKeys() error = nil, want one naming %v", tt.wantNamed)
			}
			for _, want := range tt.wantNamed {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not name %s:\n  %v", want, err)
				}
			}
			for _, unwanted := range tt.wantUnnamed {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("error names %s, which is set:\n  %v", unwanted, err)
				}
			}
			// Saying what to do is the point, not just that something is wrong.
			if !strings.Contains(err.Error(), "export") || !strings.Contains(err.Error(), "Error Tracking") {
				t.Errorf("error does not say what to do:\n  %v", err)
			}
			if apiKey != "" || appKey != "" {
				t.Errorf("returned keys alongside an error: %q, %q", apiKey, appKey)
			}
		})
	}
}
