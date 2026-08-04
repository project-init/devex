package migrate

import (
	"cmp"
	"fmt"
	"os"
	"strings"

	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/spf13/cobra"
)

// options holds the parameters required to run a database migration as an ECS task.
type options struct {
	cluster        string
	taskDef        string
	subnets        string
	securityGroups string
	container      string
	imageURI       string
	region         string
	launchType     string
}

func Command() *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run a database migration as a one-off ECS task",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if missing := opts.missingRequired(); len(missing) > 0 {
				return fmt.Errorf("missing required parameters: %s", strings.Join(missing, ", "))
			}

			return runMigration(cmd.Context(), opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.cluster, "cluster", os.Getenv("CLUSTER"), "ECS cluster name")
	flags.StringVar(&opts.taskDef, "task-def", os.Getenv("TASK_DEF"), "Task definition name")
	flags.StringVar(&opts.subnets, "subnets", os.Getenv("SUBNETS"), "Comma-separated subnet IDs")
	flags.StringVar(&opts.securityGroups, "security-groups", os.Getenv("SECURITY_GROUPS"), "Comma-separated security group IDs")
	flags.StringVar(&opts.container, "container", os.Getenv("CONTAINER"), "Container name")
	flags.StringVar(&opts.imageURI, "image-uri", os.Getenv("IMAGE_URI"), "Docker image URI")
	flags.StringVar(&opts.region, "region", cmp.Or(os.Getenv("REGION"), "us-east-1"), "AWS region")
	flags.StringVar(&opts.launchType, "launch-type", cmp.Or(os.Getenv("LAUNCH_TYPE"), string(ecstypes.LaunchTypeFargate)), "ECS launch type to run the task with")

	return cmd
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
