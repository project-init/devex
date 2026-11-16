package keygen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/ui"
)

func TestGenerateAPIKeyCountsBytes(t *testing.T) {
	key, err := generateAPIKey(32)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(decoded) != 32 || len(key) != 43 {
		t.Errorf("key %q decodes to %d bytes (%v); want 32 bytes in 43 characters", key, len(decoded), err)
	}
	if _, err := generateAPIKey(15); err == nil || !strings.Contains(err.Error(), "at least 16 bytes") {
		t.Errorf("generateAPIKey(15) error = %v, want a minimum-length error", err)
	}
}

func TestPrintKeyPrintsOnlyTheKeyWhenPiped(t *testing.T) {
	var out bytes.Buffer
	if err := printKey(ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{}), "abc123", 32); err != nil {
		t.Fatal(err)
	}
	if out.String() != "abc123\n" {
		t.Errorf("stdout = %q, want only the key", out.String())
	}
}

func TestPrintKeyJSON(t *testing.T) {
	var out bytes.Buffer
	if err := printKey(ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{Format: ui.FormatJSON}), "abc123", 32); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded["key"] != "abc123" || decoded["bytes"] != float64(32) {
		t.Errorf("stdout = %q, want key and bytes", out.String())
	}
}
