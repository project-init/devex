package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const (
	colorRed   = "\033[0;31m"
	colorGreen = "\033[0;32m"
	colorBlue  = "\033[0;34m"
	colorReset = "\033[0m"
)

func printInfo(msg string) { fmt.Fprintf(os.Stderr, "%s[INFO]%s %s\n", colorBlue, colorReset, msg) }
func printSuccess(msg string) {
	fmt.Fprintf(os.Stderr, "%s[SUCCESS]%s %s\n", colorGreen, colorReset, msg)
}
func printError(msg string) { fmt.Fprintf(os.Stderr, "%s[ERROR]%s %s\n", colorRed, colorReset, msg) }

// runMigration registers a new task definition revision pointing at the given
// image and runs it as a one-off ECS task, waiting for it to complete.
func runMigration(ctx context.Context, opts *options) error {
	launchType, capacityProviderStrategy, err := opts.placement()
	if err != nil {
		return err
	}

	printInfo("Starting database migration with parameters:")
	printInfo(fmt.Sprintf("  Cluster: %s", opts.cluster))
	printInfo(fmt.Sprintf("  Task Definition: %s", opts.taskDef))
	printInfo(fmt.Sprintf("  Subnets: %s", opts.subnets))
	printInfo(fmt.Sprintf("  Security Groups: %s", opts.securityGroups))
	printInfo(fmt.Sprintf("  Container: %s", opts.container))
	printInfo(fmt.Sprintf("  Image: %s", opts.imageURI))
	printInfo(fmt.Sprintf("  Region: %s", opts.region))
	if len(capacityProviderStrategy) > 0 {
		printInfo(fmt.Sprintf("  Capacity Provider: %s", aws.ToString(capacityProviderStrategy[0].CapacityProvider)))
	} else {
		printInfo(fmt.Sprintf("  Launch Type: %s", launchType))
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(opts.region))
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	ecsClient := ecs.NewFromConfig(cfg)

	taskDefOutput, err := ecsClient.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: &opts.taskDef,
	})
	if err != nil {
		return fmt.Errorf("failed to describe task definition: %w", err)
	}

	td := taskDefOutput.TaskDefinition
	if err := overrideImage(td.ContainerDefinitions, opts.container, opts.imageURI); err != nil {
		return fmt.Errorf("task definition %s: %w", opts.taskDef, err)
	}

	registerInput := &ecs.RegisterTaskDefinitionInput{
		Family:                  &opts.taskDef,
		ContainerDefinitions:    td.ContainerDefinitions,
		Cpu:                     td.Cpu,
		Memory:                  td.Memory,
		NetworkMode:             td.NetworkMode,
		RequiresCompatibilities: td.RequiresCompatibilities,
		TaskRoleArn:             td.TaskRoleArn,
		ExecutionRoleArn:        td.ExecutionRoleArn,
		Volumes:                 td.Volumes,
		PlacementConstraints:    td.PlacementConstraints,
		RuntimePlatform:         td.RuntimePlatform,
		EphemeralStorage:        td.EphemeralStorage,
	}

	registerOutput, err := ecsClient.RegisterTaskDefinition(ctx, registerInput)
	if err != nil {
		return fmt.Errorf("failed to register task definition: %w", err)
	}

	revision := registerOutput.TaskDefinition.Revision
	fullTaskDef := fmt.Sprintf("%s:%d", opts.taskDef, revision)
	printInfo(fmt.Sprintf("Registered new task definition: %s", fullTaskDef))

	subnetList := strings.Split(opts.subnets, ",")
	sgList := strings.Split(opts.securityGroups, ",")

	runOutput, err := ecsClient.RunTask(ctx, &ecs.RunTaskInput{
		Cluster:                  &opts.cluster,
		TaskDefinition:           &fullTaskDef,
		LaunchType:               launchType,
		CapacityProviderStrategy: capacityProviderStrategy,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        subnetList,
				SecurityGroups: sgList,
				AssignPublicIp: ecstypes.AssignPublicIpDisabled,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to run task: %w", err)
	}

	if len(runOutput.Tasks) == 0 {
		if len(runOutput.Failures) > 0 {
			data, _ := json.MarshalIndent(runOutput.Failures, "", "  ")
			printError(string(data))
		}
		return fmt.Errorf("failed to start task: no tasks returned")
	}

	taskARN := aws.ToString(runOutput.Tasks[0].TaskArn)
	printInfo(fmt.Sprintf("Task started with ARN: %s", taskARN))
	printInfo("Waiting for task to complete...")

	// A task on a capacity provider can wait in PROVISIONING for a scale-out, so poll often enough to
	// notice the stop promptly.
	waiter := ecs.NewTasksStoppedWaiter(ecsClient, func(o *ecs.TasksStoppedWaiterOptions) {
		o.MaxDelay = 30 * time.Second
	})
	describeInput := &ecs.DescribeTasksInput{Cluster: &opts.cluster, Tasks: []string{taskARN}}
	var task ecstypes.Task
	describeOutput, err := waiter.WaitForOutput(ctx, describeInput, 30*time.Minute)
	if err == nil {
		task = describeOutput.Tasks[0]
	} else {
		var settleErr error
		task, settleErr = settleAfterWait(ctx, ecsClient, describeInput, opts.container)
		if settleErr != nil {
			return fmt.Errorf("error waiting for task to stop: %w; %w", err, settleErr)
		}
	}

	exitCode, err := migrationExitCode(task, opts.container)
	if err != nil {
		return err
	}

	if exitCode != 0 {
		logGroup := fmt.Sprintf("/ecs/%s", opts.taskDef)
		printInfo(fmt.Sprintf("Fetching recent logs from %s...", logGroup))
		fetchLogs(ctx, cfg, logGroup)
		return fmt.Errorf("migration task failed with exit code %d", exitCode)
	}

	printSuccess("Migration task completed successfully")
	return nil
}

// overrideImage points the named container at image. It fails before anything runs when no container
// has that name, rather than letting the task run its old image.
func overrideImage(containers []ecstypes.ContainerDefinition, name, image string) error {
	for i, container := range containers {
		if aws.ToString(container.Name) == name {
			containers[i].Image = &image

			return nil
		}
	}

	return fmt.Errorf("no %s container", name)
}

// settleAfterWait reports on a task the wait gave up on. A stopped task, one ECS abandoned before it
// started, or one whose container already exited still reports its result. Anything else is left
// alone, since stopping it could interrupt a migration, and the error names its status for the
// operator to check before retrying. Taking only DescribeTasks keeps it from stopping anything.
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

	return ecstypes.Task{}, fmt.Errorf("task %s is still %s and may yet change the database, so check it before retrying",
		aws.ToString(task.TaskArn), aws.ToString(task.LastStatus))
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

func fetchLogs(ctx context.Context, cfg aws.Config, logGroup string) {
	logsClient := cloudwatchlogs.NewFromConfig(cfg)

	since := time.Now().Add(-10 * time.Minute).UnixMilli()
	input := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName: &logGroup,
		StartTime:    &since,
	}

	output, err := logsClient.FilterLogEvents(ctx, input)
	if err != nil {
		printInfo(fmt.Sprintf("Could not fetch logs: %v", err))
		return
	}

	for _, event := range output.Events {
		fmt.Println(aws.ToString(event.Message))
	}
}
