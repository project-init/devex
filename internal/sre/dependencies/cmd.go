package dependencies

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/dependencies/bsr"
	"github.com/project-init/devex/internal/sre/dependencies/gosync"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/registry"
	"github.com/spf13/cobra"
)

const (
	ecosystemGo   = "go"
	ecosystemMise = "mise"
	ecosystemBuf  = "buf"
)

func Command() *cobra.Command {
	depsCmd := &cobra.Command{
		Use:         "dependencies",
		Short:       "Manage dependencies for the repository",
		Annotations: map[string]string{config.OptionalAnnotation: "true"},
	}
	depsCmd.AddCommand(upgradeCommand(), checkCommand())

	return depsCmd
}

func upgradeCommand() *cobra.Command {
	var o upgradeOptions
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade dependencies, keeping every Go version pin on one version",
		Long: `Upgrade mise tools, Go modules, and Buf dependencies.

--go moves every Go version pin (mise, go.mod, go.work, Dockerfile golang images, setup-go,
.go-version, .tool-versions, and declared pins) to one version, then runs go get -u and
go mod tidy under exactly that toolchain.

--mise bumps every mise tool except Go, and --buf moves the remote plugin versions and git
input tags in buf.gen.yaml, then regenerates. A policy per tool or pin in
.sre/dependencies.yaml caps each at minor or patch, or pins it. A git input whose repository
a go.mod requires follows that module's version instead.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := dependenciesConfig(cmd)
			if o.all {
				if err := o.enable(cfg.Upgrade); err != nil {
					return err
				}
			}
			if !o.goFlag && !o.miseFlag && !o.bufFlag {
				return errors.New("choose at least one of --go, --mise, --buf, or --all")
			}
			if o.goVersion != "" && !o.goFlag {
				return errors.New("--go-version requires --go")
			}

			return runUpgrade(cmd.Context(), cmd.OutOrStdout(), o, cfg, defaultEnvironment(cmd))
		},
	}

	cmd.Flags().BoolVar(&o.goFlag, ecosystemGo, false, "Upgrade Go modules and sync every Go version pin")
	cmd.Flags().BoolVar(&o.miseFlag, ecosystemMise, false, "Upgrade mise tools within their policies; Go stays put unless --go is also set")
	cmd.Flags().BoolVar(&o.bufFlag, ecosystemBuf, false, "Upgrade buf.gen.yaml plugins and inputs within their policies, regenerate, and refresh buf.lock")
	cmd.Flags().BoolVar(&o.all, "all", false, "Upgrade the ecosystems listed in dependencies.upgrade")
	cmd.Flags().StringVar(&o.goVersion, "go-version", "", "Move Go to this exact release, overriding the target and any existing drift")
	cmd.Flags().BoolVar(&o.dryRun, "show-commands", false, "Plan and verify without writing: print each pin change and command")

	return cmd
}

func checkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify Go version pins and linked buf.gen tags agree, offline",
		Long: `Fail when Go version pins disagree, when a go.mod go directive sits above the
toolchain, when a go.work go directive sits below a module it uses, when the directive
policy in .sre/dependencies.yaml does not hold, or when a buf.gen.yaml git input that
follows a go.mod module sits on another tag or takes a policy. Run it in pull request CI to
catch drift from Dependabot and hand edits.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCheck(cmd.OutOrStdout(), dependenciesConfig(cmd), ".")
		},
	}
	// check always covers Go and linked buf inputs, so --go is accepted but not required.
	cmd.Flags().Bool(ecosystemGo, false, "Check Go version pins (the default)")

	return cmd
}

type upgradeOptions struct {
	goFlag    bool
	miseFlag  bool
	bufFlag   bool
	all       bool
	goVersion string
	dryRun    bool
}

func (o *upgradeOptions) enable(ecosystems []string) error {
	if len(ecosystems) == 0 {
		return errors.New("--all reads dependencies.upgrade from .sre config, which lists nothing")
	}
	for _, e := range ecosystems {
		switch e {
		case ecosystemGo:
			o.goFlag = true
		case ecosystemMise:
			o.miseFlag = true
		case ecosystemBuf:
			o.bufFlag = true
		default:
			return fmt.Errorf("dependencies.upgrade lists %q; valid values are go, mise, and buf", e)
		}
	}

	return nil
}

func dependenciesConfig(cmd *cobra.Command) config.DependenciesConfiguration {
	if cfg, ok := config.GetConfig(cmd.Context()); ok && cfg != nil {
		return cfg.Dependencies
	}

	return config.DependenciesConfiguration{}
}

// networkTimeout bounds each go.dev, container registry, and Buf registry request and each git
// tag listing, so a hung endpoint fails the run instead of stalling CI.
const networkTimeout = 30 * time.Second

func hasMise() bool {
	_, err := exec.LookPath("mise")

	return err == nil
}

func defaultEnvironment(cmd *cobra.Command) environment {
	client := &http.Client{Timeout: networkTimeout}

	return environment{
		Root:     ".",
		Run:      gosync.ExecRunner{Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()},
		Resolver: goversion.Resolver{Client: client},
		Registry: registry.Client{HTTP: client},
		Plugins:  bsr.Client{HTTP: client},
		HasMise:  hasMise(),
	}
}
