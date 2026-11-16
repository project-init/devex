package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/project-init/devex/internal/ui"
)

// ecsAPI is the slice of the ECS client a migration uses.
type ecsAPI interface {
	DescribeTaskDefinition(context.Context, *ecs.DescribeTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error)
	RegisterTaskDefinition(context.Context, *ecs.RegisterTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
	RunTask(context.Context, *ecs.RunTaskInput, ...func(*ecs.Options)) (*ecs.RunTaskOutput, error)
	ecs.DescribeTasksAPIClient
}

// logsAPI is the slice of the CloudWatch Logs client that streams a task's logs.
type logsAPI interface {
	cloudwatchlogs.GetLogEventsAPIClient
}

// clients builds the AWS clients for a migration; tests replace it.
type clients func(ctx context.Context, region string) (ecsAPI, func(region string) logsAPI, error)

func awsClients(ctx context.Context, region string) (ecsAPI, func(string) logsAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	logsFor := func(logRegion string) logsAPI {
		return cloudwatchlogs.NewFromConfig(cfg, func(o *cloudwatchlogs.Options) {
			if logRegion != "" {
				o.Region = logRegion
			}
		})
	}
	return ecs.NewFromConfig(cfg), logsFor, nil
}

// runMigration registers a new revision of the task definition that points the migration container
// at the image, runs it as a one-off ECS task, and streams its logs until it stops.
func runMigration(ctx context.Context, u *ui.UI, opts *options, newClients clients) error {
	return runMigrationEvery(ctx, u, opts, newClients, defaultPollInterval)
}

// runMigrationEvery is runMigration polling the task every poll; tests shorten it.
func runMigrationEvery(ctx context.Context, u *ui.UI, opts *options, newClients clients, poll time.Duration) error {
	launchType, capacityProviderStrategy, err := opts.placement()
	if err != nil {
		return err
	}
	placement := fmt.Sprintf("launch type %s", launchType)
	if opts.capacityProvider != "" {
		placement = "capacity provider " + opts.capacityProvider
	}
	u.Status("Migrating with task definition %s in cluster %s (%s, %s)", opts.taskDef, opts.cluster, opts.region, placement)

	ecsClient, logsFor, err := newClients(ctx, opts.region)
	if err != nil {
		return err
	}
	described, err := ecsClient.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{TaskDefinition: &opts.taskDef})
	if err != nil {
		return fmt.Errorf("describe task definition %s: %w", opts.taskDef, err)
	}
	td := described.TaskDefinition
	container := findDefinition(td.ContainerDefinitions, opts.container)
	if container == nil {
		// Fail before anything runs, rather than letting the task run its old image.
		return fmt.Errorf("task definition %s has no %s container", opts.taskDef, opts.container)
	}
	oldImage := aws.ToString(container.Image)
	container.Image = &opts.imageURI
	logs := logTargetFor(container)

	if opts.dryRun {
		u.Printf("Would register a new revision of %s from %s:\n", aws.ToString(td.Family), aws.ToString(td.TaskDefinitionArn))
		u.Printf("  container %s image: %s -> %s\n", opts.container, oldImage, opts.imageURI)
		u.Printf("Would run it in cluster %s on %s, subnets %s, security groups %s.\n", opts.cluster, placement, opts.subnets, opts.securityGroups)
		u.Status("Dry run: nothing registered or run.")
		return nil
	}

	registered, err := ecsClient.RegisterTaskDefinition(ctx, registerInputFrom(td))
	if err != nil {
		return fmt.Errorf("register task definition: %w", err)
	}
	revision := aws.ToString(registered.TaskDefinition.TaskDefinitionArn)
	u.Status("Registered %s with image %s", revision, opts.imageURI)

	runOutput, err := ecsClient.RunTask(ctx, &ecs.RunTaskInput{
		Cluster:                  &opts.cluster,
		TaskDefinition:           &revision,
		LaunchType:               launchType,
		CapacityProviderStrategy: capacityProviderStrategy,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        strings.Split(opts.subnets, ","),
				SecurityGroups: strings.Split(opts.securityGroups, ","),
				AssignPublicIp: ecstypes.AssignPublicIpDisabled,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("run task: %w", err)
	}
	if len(runOutput.Tasks) == 0 {
		data, _ := json.Marshal(runOutput.Failures)
		return fmt.Errorf("ECS started no task: %s", data)
	}
	taskARN := aws.ToString(runOutput.Tasks[0].TaskArn)
	u.Status("Started task %s", taskARN)

	var stream *logStream
	if logs != nil {
		stream = &logStream{client: logsFor(logs.region), group: logs.group, stream: logs.stream(opts.container, taskARN)}
	} else {
		u.Warn("streaming no logs: container %s needs the awslogs log driver with awslogs-stream-prefix", opts.container)
	}
	watch := &taskWatch{
		client:    ecsClient,
		logs:      stream,
		input:     &ecs.DescribeTasksInput{Cluster: &opts.cluster, Tasks: []string{taskARN}},
		container: opts.container,
		timeout:   opts.waitTimeout,
		poll:      poll,
		missing:   time.Minute,
	}
	task, err := watch.run(ctx, u)
	if err != nil {
		// The watch ended before the task stopped, whether on Ctrl-C, a timeout, or a failed check.
		u.Warn("task %s may still be running in ECS and change the database; check it before retrying", taskARN)
		return err
	}

	exitCode, err := migrationExitCode(task, opts.container)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("migration task failed with exit code %d", exitCode)
	}
	u.Success("Migration task completed")
	return nil
}

// settleAfterWait reports on a task the wait gave up on. A stopped task, one ECS abandoned before it
// started, or one whose container already exited still reports its result. Anything else is left
// alone, since stopping it could interrupt a migration, and the error names its status. Taking only DescribeTasks keeps it from stopping anything.
func settleAfterWait(ctx context.Context, ecsClient ecs.DescribeTasksAPIClient, describeInput *ecs.DescribeTasksInput, containerName string) (ecstypes.Task, error) {
	current, err := ecsClient.DescribeTasks(ctx, describeInput)
	if err != nil {
		return ecstypes.Task{}, fmt.Errorf("describing the task failed too: %w", err)
	}

	if len(current.Tasks) == 0 {
		data, _ := json.Marshal(current.Failures)

		return ecstypes.Task{}, fmt.Errorf("ECS no longer finds task %s: %s", strings.Join(describeInput.Tasks, ", "), data)
	}

	task := current.Tasks[0]
	container := findContainer(task, containerName)
	stopped := aws.ToString(task.LastStatus) == string(ecstypes.DesiredStatusStopped)
	// A task ECS is winding down before it ever started, such as one that never got capacity, can't run.
	abandoned := aws.ToString(task.DesiredStatus) == string(ecstypes.DesiredStatusStopped) && task.StartedAt == nil
	if stopped || abandoned || (container != nil && container.ExitCode != nil) {
		return task, nil
	}

	return ecstypes.Task{}, fmt.Errorf("task %s is still %s", aws.ToString(task.TaskArn), aws.ToString(task.LastStatus))
}

func findContainer(task ecstypes.Task, containerName string) *ecstypes.Container {
	for i := range task.Containers {
		if aws.ToString(task.Containers[i].Name) == containerName {
			return &task.Containers[i]
		}
	}

	return nil
}

// migrationExitCode returns the named container's exit code from a stopped task. Without one, it
// reports ECS's stop reason: the task never got capacity, or its host failed mid-run.
func migrationExitCode(task ecstypes.Task, containerName string) (int32, error) {
	container := findContainer(task, containerName)
	if container == nil {
		return 0, fmt.Errorf("migration task has no %s container", containerName)
	}

	if container.ExitCode != nil {
		return *container.ExitCode, nil
	}

	reason := strings.Join(slices.DeleteFunc([]string{string(task.StopCode), aws.ToString(task.StoppedReason), aws.ToString(container.Reason)},
		func(part string) bool { return part == "" }), ": ")
	if reason == "" {
		reason = "no reason given"
	}

	if task.StartedAt == nil {
		return 0, fmt.Errorf("migration task stopped before %s ran: %s", containerName, reason)
	}

	return 0, fmt.Errorf("migration task stopped mid-run without an exit code, so check the database before retrying: %s", reason)
}
