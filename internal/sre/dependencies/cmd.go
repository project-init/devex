package dependencies

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	depsCmd := &cobra.Command{
		Use:   "dependencies",
		Short: "Manage dependencies for the repository",
	}

	var goFlag, miseFlag, bufFlag, allFlag, showCommands bool

	upgradeCmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade specified dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			// If --all is passed, fallback to the config file flags
			if allFlag {
				if cfg, ok := config.GetConfig(cmd.Context()); ok && cfg != nil {
					if cfg.Dependencies.Go != nil && *cfg.Dependencies.Go {
						goFlag = true
					}
					if cfg.Dependencies.Mise != nil && *cfg.Dependencies.Mise {
						miseFlag = true
					}
					if cfg.Dependencies.Buf != nil && *cfg.Dependencies.Buf {
						bufFlag = true
					}
				} else {
					fmt.Println("⚠️  WARNING: Could not parse configuration for --all!")
				}
			}

			if showCommands {
				if miseFlag {
					fmt.Println("mise upgrade --bump")
				}
				if goFlag {
					fmt.Println("go get -u ./...")
					fmt.Println("go mod tidy")
				}
				if bufFlag {
					fmt.Println("buf dep update")
				}
				return nil
			}

			if miseFlag {
				fmt.Println("Upgrading local mise tooling...")
				runCmd("mise", "upgrade", "--bump")
			}
			if goFlag {
				fmt.Println("Upgrading Go modules...")
				runCmd("go", "get", "-u", "./...")
				runCmd("go", "mod", "tidy")
			}
			if bufFlag {
				fmt.Println("Upgrading Buf remote plugins...")
				runCmd("buf", "dep", "update")
			}

			return nil
		},
	}

	upgradeCmd.Flags().BoolVar(&goFlag, "go", false, "Upgrade native go dependencies")
	upgradeCmd.Flags().BoolVar(&miseFlag, "mise", false, "Upgrade global/local mise tools")
	upgradeCmd.Flags().BoolVar(&bufFlag, "buf", false, "Upgrade buf schema dependencies")
	upgradeCmd.Flags().BoolVar(&allFlag, "all", false, "Upgrade intelligently based on .sre/deps.yaml configuration")
	upgradeCmd.Flags().BoolVar(&showCommands, "show-commands", false, "Print terminal targets without actually executing them")

	depsCmd.AddCommand(upgradeCmd)
	return depsCmd
}

func runCmd(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running %s %v: %v\n", name, args, err)
	}
}
