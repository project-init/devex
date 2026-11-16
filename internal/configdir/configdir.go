// Package configdir finds and loads a tool's directory of YAML configuration files, such as
// .sre or .localize.
package configdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/project-init/devex/internal/strictyaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// FlagName is the persistent flag that overrides the configuration directory.
const FlagName = "config-dir"

// legacyFlagName is the camel-case spelling that FlagName replaced; it keeps working.
const legacyFlagName = "configDir"

// AddFlag registers --config-dir on cmd and accepts --configDir as an alias for it.
func AddFlag(cmd *cobra.Command, target *string, defaultDir string) {
	cmd.PersistentFlags().StringVar(target, FlagName, "", fmt.Sprintf("config directory (default is %s)", defaultDir))
	cmd.SetGlobalNormalizationFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == legacyFlagName {
			return FlagName
		}
		return pflag.NormalizedName(name)
	})
}

// Resolve returns explicit when it names a directory, or defaultDir when it exists.
func Resolve(explicit, defaultDir string) (string, error) {
	if explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("config dir %q not found: %w", explicit, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("config dir %q is a file; pass its directory", explicit)
		}
		return explicit, nil
	}
	if _, err := os.Stat(defaultDir); err != nil {
		return "", fmt.Errorf("no config found: pass --%s or create %s", FlagName, defaultDir)
	}
	return defaultDir, nil
}

// Load decodes every *.yaml file in dir into out, in name order, so a later file overrides an
// earlier one. A key out does not declare, or a second YAML document in one file, fails with the
// file's path. Load skips an empty file and any file with one of foreignKeys at its top level,
// because another tool owns that file.
func Load(dir string, out any, foreignKeys ...string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("list config files in %s: %w", dir, err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if len(foreignKeys) > 0 && strictyaml.HasTopLevelKey(raw, foreignKeys...) {
			continue
		}
		if err := strictyaml.Decode(raw, out); err != nil && !errors.Is(err, strictyaml.ErrEmpty) {
			return fmt.Errorf("%s: %w", file, err)
		}
	}
	return nil
}
