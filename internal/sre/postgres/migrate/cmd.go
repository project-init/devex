package migrate

import (
	"cmp"
	"fmt"
	"os"
	"strings"

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

	return cmd
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
