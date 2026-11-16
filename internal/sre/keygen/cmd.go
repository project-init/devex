package keygen

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

const (
	defaultLength = 32
	minLength     = 16
)

func Command() *cobra.Command {
	var length int
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Print a random, URL-safe API key",
		Long: "Print a random API key from crypto/rand, base64url-encoded without padding. --length (or keygen.length " +
			"in the sre config) counts random bytes, not characters: 32 bytes make a 43-character key. When stdout " +
			"is a pipe or file, the command prints only the key, so KEY=$(devex sre keygen) captures it.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{config.OptionalAnnotation: "true", ui.JSONAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg, _ := config.GetConfig(cmd.Context()); !cmd.Flags().Changed("length") && cfg != nil && cfg.Keygen.Length != nil {
				length = *cfg.Keygen.Length
			}
			apiKey, err := generateAPIKey(length)
			if err != nil {
				return err
			}
			return printKey(ui.New(cmd), apiKey, length)
		},
	}
	cmd.Flags().IntVar(&length, "length", defaultLength, "random bytes in the key, at least 16; overrides keygen.length")
	return cmd
}

func printKey(u *ui.UI, apiKey string, length int) error {
	switch {
	case u.JSON():
		return u.WriteJSON(map[string]any{"key": apiKey, "bytes": length})
	case ui.IsTerminal(u.Out()):
		u.Printf("API key (%d bytes): %s\n", length, apiKey)
	default:
		u.Printf("%s\n", apiKey)
	}
	return nil
}

func generateAPIKey(nBytes int) (string, error) {
	if nBytes < minLength {
		return "", fmt.Errorf("key length must be at least %d bytes, got %d (from --length or keygen.length)", minLength, nBytes)
	}

	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	// URL-safe, no "+" "/" "=", good for config files, headers, URLs, etc.
	return base64.RawURLEncoding.EncodeToString(b), nil
}
