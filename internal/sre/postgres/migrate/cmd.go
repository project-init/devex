package migrate

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

// options holds the parameters required to run a database migration as an ECS task.
type options struct {
	cluster          string
	taskDef          string
	subnets          string
	securityGroups   string
	container        string
	imageURI         string
	region           string
	launchType       string
	capacityProvider string
	waitTimeout      time.Duration
	dryRun           bool
}

// envPrefix namespaces the environment variables each flag falls back to.
const envPrefix = "DEVEX_"

// envVariable is the variable, before envPrefix, that a flag falls back to: --task-def reads TASK_DEF.
func envVariable(flag string) string {
	return strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

func Command() *cobra.Command {
	opts := &options{}
	var envFlags []string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run a database migration as a one-off ECS task",
		Long: "Register a revision of the task definition that runs the migration container on --image-uri, run it " +
			"as a one-off ECS task, and stream its logs until it stops. Value flags fall back to DEVEX_-prefixed " +
			"environment variables, such as DEVEX_CLUSTER. --dry-run shows the change without registering or running.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			u := ui.New(cmd)
			if err := checkLegacyEnv(cmd, envFlags); err != nil {
				return err
			}
			if missing := opts.missingRequired(); len(missing) > 0 {
				return fmt.Errorf("missing required parameters: %s", strings.Join(missing, ", "))
			}
			if opts.waitTimeout <= 0 {
				return fmt.Errorf("--wait-timeout must be positive, got %s", opts.waitTimeout)
			}

			return runMigration(cmd.Context(), u, opts, awsClients)
		},
	}

	flags := cmd.Flags()
	// envString registers a string flag that defaults to its DEVEX_ variable, then to fallback.
	envString := func(target *string, name, fallback, usage string) {
		envFlags = append(envFlags, name)
		flags.StringVar(target, name, cmp.Or(os.Getenv(envPrefix+envVariable(name)), fallback), usage)
	}
	envString(&opts.cluster, "cluster", "", "ECS cluster name")
	envString(&opts.taskDef, "task-def", "", "task definition: family, family:revision, or ARN")
	envString(&opts.subnets, "subnets", "", "comma-separated subnet IDs")
	envString(&opts.securityGroups, "security-groups", "", "comma-separated security group IDs")
	envString(&opts.container, "container", "", "name of the container that runs the migration")
	envString(&opts.imageURI, "image-uri", "", "image URI to run the migration with")
	envString(&opts.region, "region", "us-east-1", "AWS region")
	envString(&opts.launchType, "launch-type", "", "ECS launch type to run the task with; FARGATE when neither this nor --capacity-provider is set")
	envString(&opts.capacityProvider, "capacity-provider", "", "ECS capacity provider to run the task on, instead of a launch type")
	flags.DurationVar(&opts.waitTimeout, "wait-timeout", 30*time.Minute, "how long to wait for the task to stop")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "show the task definition change without registering or running anything")

	return cmd
}

// checkLegacyEnv fails when an unprefixed variable that earlier releases read is set, and neither
// its DEVEX_ variable nor its flag replaces it. Ignoring it could quietly migrate another region.
func checkLegacyEnv(cmd *cobra.Command, flags []string) error {
	var errs []error
	for _, flag := range flags {
		variable := envVariable(flag)
		if os.Getenv(variable) != "" && os.Getenv(envPrefix+variable) == "" && !cmd.Flags().Changed(flag) {
			errs = append(errs, fmt.Errorf("%s is set, but migrate reads %s%s now; rename it or pass --%s", variable, envPrefix, variable, flag))
		}
	}
	return errors.Join(errs...)
}

// placement chooses how RunTask places the task. A named capacity provider lets a full cluster scale
// out: the task waits in PROVISIONING for capacity rather than failing. Otherwise the launch type
// places it.
func (o *options) placement() (ecstypes.LaunchType, []ecstypes.CapacityProviderStrategyItem, error) {
	if o.capacityProvider == "" {
		if o.launchType == "" {
			return ecstypes.LaunchTypeFargate, nil, nil
		}

		launchType, err := o.resolveLaunchType()

		return launchType, nil, err
	}

	if o.launchType != "" {
		return "", nil, fmt.Errorf("set --capacity-provider (%sCAPACITY_PROVIDER) or --launch-type (%sLAUNCH_TYPE), not both", envPrefix, envPrefix)
	}

	return "", []ecstypes.CapacityProviderStrategyItem{{CapacityProvider: &o.capacityProvider, Weight: 1}}, nil
}

// resolveLaunchType normalizes the launch type flag and rejects values ECS does not accept.
// The task definition must declare the resulting launch type in its requiresCompatibilities,
// otherwise RunTask fails with InvalidParameterException.
func (o *options) resolveLaunchType() (ecstypes.LaunchType, error) {
	valid := ecstypes.LaunchTypeFargate.Values()

	want := strings.ToUpper(o.launchType)
	for _, launchType := range valid {
		if string(launchType) == want {
			return launchType, nil
		}
	}

	names := make([]string, 0, len(valid))
	for _, launchType := range valid {
		names = append(names, string(launchType))
	}

	return "", fmt.Errorf("invalid --launch-type %q: must be one of %s", o.launchType, strings.Join(names, ", "))
}

// missingRequired returns the names of required flags that were not provided.
func (o *options) missingRequired() []string {
	required := []struct {
		value string
		name  string
	}{
		{o.cluster, "--cluster"},
		{o.taskDef, "--task-def"},
		{o.subnets, "--subnets"},
		{o.securityGroups, "--security-groups"},
		{o.container, "--container"},
		{o.imageURI, "--image-uri"},
	}

	var missing []string
	for _, f := range required {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	return missing
}
