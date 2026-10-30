package slos

import (
	"strings"
	"testing"
)

func TestLoadKeys(t *testing.T) {
	tests := map[string]struct {
		env            map[string]string
		apiKey, appKey string
		wantErr        []string
	}{
		"both set": {
			env:    map[string]string{apiKeyEnv: "api", appKeyEnv: "app"},
			apiKey: "api", appKey: "app",
		},
		"no app key": {
			env:     map[string]string{apiKeyEnv: "api"},
			wantErr: []string{appKeyEnv},
		},
		"neither": {
			wantErr: []string{apiKeyEnv, appKeyEnv},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			apiKey, appKey, err := loadKeys(func(k string) string { return tt.env[k] })
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("loadKeys: want an error naming %v", tt.wantErr)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("loadKeys error %q does not name %s", err, want)
					}
				}
				if apiKey != "" || appKey != "" {
					t.Error("loadKeys returned a key with an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadKeys: %v", err)
			}
			if apiKey != tt.apiKey || appKey != tt.appKey {
				t.Errorf("loadKeys got %q, %q, want %q, %q", apiKey, appKey, tt.apiKey, tt.appKey)
			}
		})
	}
}
