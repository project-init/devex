package keygen

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Print an API Key based on the sre configuration file.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}

			length := 32
			if cfg.Keygen.Length != nil {
				length = *cfg.Keygen.Length
			}

			apiKey, err := generateAPIKey(length)
			if err != nil {
				return err
			}

			fmt.Printf("Keygen API Key: %s\n", apiKey)
			return nil
		},
	}
	return cmd
}

func generateAPIKey(nBytes int) (string, error) {
	if nBytes < 16 {
		return "", fmt.Errorf("nBytes too small: got %d, want at least 16", nBytes)
	}

	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	// URL-safe, no "+" "/" "=", good for config files, headers, URLs, etc.
	return base64.RawURLEncoding.EncodeToString(b), nil
}
