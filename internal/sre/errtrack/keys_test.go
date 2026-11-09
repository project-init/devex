package errtrack

import (
	"strings"
	"testing"
)

func TestLoadKeys(t *testing.T) {
	tests := map[string]struct {
		env            map[string]string
		apiKey, appKey string
		wantErr        []string // env var names the message must name
	}{
		"both set": {
			env:    map[string]string{apiKeyEnv: "api", appKeyEnv: "app"},
			apiKey: "api", appKey: "app",
		},

		// Exactly one set is the typo case: the pair looks configured at a
		// glance, so the message has to say which one is missing.
		"no app key": {
			env:     map[string]string{apiKeyEnv: "api"},
			wantErr: []string{appKeyEnv},
		},
		"no api key": {
			env:     map[string]string{appKeyEnv: "app"},
			wantErr: []string{apiKeyEnv},
		},

		"neither": {
			wantErr: []string{apiKeyEnv, appKeyEnv},
		},

		// An exported but empty variable is the same as unset: a shell that
		// ran `export DD_APP_KEY=` has not configured anything.
		"app key exported empty": {
			env:     map[string]string{apiKeyEnv: "api", appKeyEnv: ""},
			wantErr: []string{appKeyEnv},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// A map-backed getenv, so no test touches the process environment
			// and none of them depend on running in any particular order.
			apiKey, appKey, err := loadKeys(func(k string) string { return tt.env[k] })

			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("loadKeys() error = nil, want one naming %v", tt.wantErr)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error does not name %s:\n  %v", want, err)
					}
				}
				// The message is the point of this function, so check it says
				// what to do rather than only that something is wrong.
				if !strings.Contains(err.Error(), "export") {
					t.Errorf("error does not tell the user to export anything:\n  %v", err)
				}
				if !strings.Contains(err.Error(), "Error Tracking") {
					t.Errorf("error does not say which Datadog access is needed:\n  %v", err)
				}
				if apiKey != "" || appKey != "" {
					t.Errorf("returned keys alongside an error: %q, %q", apiKey, appKey)
				}

				return
			}

			if err != nil {
				t.Fatalf("loadKeys() error = %v", err)
			}
			if apiKey != tt.apiKey || appKey != tt.appKey {
				t.Errorf("loadKeys() = %q, %q, want %q, %q", apiKey, appKey, tt.apiKey, tt.appKey)
			}
		})
	}
}

// TestLoadKeysNamesOnlyTheMissingOne guards against the lazy fix for the
// one-missing case, which is to name both variables every time. That reads as
// "neither is set" to someone who has one exported, and sends them looking at
// the wrong one.
func TestLoadKeysNamesOnlyTheMissingOne(t *testing.T) {
	_, _, err := loadKeys(func(k string) string {
		return map[string]string{apiKeyEnv: "api"}[k]
	})
	if err == nil {
		t.Fatal("loadKeys() error = nil, want one")
	}
	if strings.Contains(err.Error(), apiKeyEnv) {
		t.Errorf("error names %s, which is set:\n  %v", apiKeyEnv, err)
	}
}
