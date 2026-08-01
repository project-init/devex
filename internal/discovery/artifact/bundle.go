package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/project-init/devex/internal/discovery/domain"
	"gopkg.in/yaml.v3"
)

const WorkBreakdownFile = "work-breakdown.yaml"

type Bundle struct {
	Directory        string
	WorkBreakdown    *domain.WorkBreakdown
	DiscoveryContent []byte
	WorkBreakdownRaw []byte
}

func Load(directory string) (*Bundle, error) {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	workBreakdownPath := filepath.Join(absDirectory, WorkBreakdownFile)
	raw, err := os.ReadFile(workBreakdownPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", workBreakdownPath, err)
	}

	var workBreakdown domain.WorkBreakdown
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&workBreakdown); err != nil {
		return nil, fmt.Errorf("decode %s: %w", workBreakdownPath, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode %s: multiple YAML documents are not supported", workBreakdownPath)
		}
		return nil, fmt.Errorf("decode %s: %w", workBreakdownPath, err)
	}
	if err := workBreakdown.Validate(); err != nil {
		return nil, err
	}

	documentPath, err := safeDocumentPath(absDirectory, workBreakdown.Discovery.Document)
	if err != nil {
		return nil, err
	}
	discoveryContent, err := os.ReadFile(documentPath)
	if err != nil {
		return nil, fmt.Errorf("read discovery document %s: %w", documentPath, err)
	}
	if err := validateDiscoveryDocument(discoveryContent); err != nil {
		return nil, err
	}

	return &Bundle{
		Directory:        absDirectory,
		WorkBreakdown:    &workBreakdown,
		DiscoveryContent: discoveryContent,
		WorkBreakdownRaw: raw,
	}, nil
}

func (b *Bundle) Digest() string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("discovery-document\x00"))
	_, _ = hash.Write(b.DiscoveryContent)
	_, _ = hash.Write([]byte("\x00work-breakdown\x00"))
	_, _ = hash.Write(b.WorkBreakdownRaw)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func safeDocumentPath(directory string, document string) (string, error) {
	if filepath.IsAbs(document) {
		return "", fmt.Errorf("discovery.document must be relative to the bundle")
	}
	cleaned := filepath.Clean(document)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("discovery.document must stay inside the bundle")
	}
	return filepath.Join(directory, cleaned), nil
}

var requiredHeadings = []string{
	"## Goals",
	"## Useful Context",
	"## Proposed Approach",
	"### Benefits",
	"### Considerations",
	"### Alternatives",
	"## Phasing",
	"## Ownership and Access",
	"## Assumptions",
	"## Unknowns",
}

func validateDiscoveryDocument(content []byte) error {
	text := string(content)
	if !strings.HasPrefix(strings.TrimSpace(text), "# ") {
		return fmt.Errorf("discovery document must start with an H1 title")
	}
	var missing []string
	for _, heading := range requiredHeadings {
		if !strings.Contains(text, "\n"+heading+"\n") {
			missing = append(missing, heading)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("discovery document is missing required headings: %s", strings.Join(missing, ", "))
	}
	return nil
}
