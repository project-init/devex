package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
// image and runs it as a one-off Fargate task, waiting for it to complete.
func runMigration(ctx context.Context, opts *options) error {
	printInfo("Starting database migration with parameters:")
	printInfo(fmt.Sprintf("  Cluster: %s", opts.cluster))
	printInfo(fmt.Sprintf("  Task Definition: %s", opts.taskDef))
	printInfo(fmt.Sprintf("  Subnets: %s", opts.subnets))
	printInfo(fmt.Sprintf("  Security Groups: %s", opts.securityGroups))
	printInfo(fmt.Sprintf("  Container: %s", opts.container))
	printInfo(fmt.Sprintf("  Image: %s", opts.imageURI))
	printInfo(fmt.Sprintf("  Region: %s", opts.region))

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
	for i, c := range td.ContainerDefinitions {
		if aws.ToString(c.Name) == opts.container {
			td.ContainerDefinitions[i].Image = &opts.imageURI
		}
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
		Cluster:        &opts.cluster,
		TaskDefinition: &fullTaskDef,
		LaunchType:     ecstypes.LaunchTypeFargate,
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

	waiter := ecs.NewTasksStoppedWaiter(ecsClient)
	describeOutput, err := waiter.WaitForOutput(ctx, &ecs.DescribeTasksInput{
		Cluster: &opts.cluster,
		Tasks:   []string{taskARN},
	}, 30*time.Minute)
	if err != nil {
		return fmt.Errorf("error waiting for task to stop: %w", err)
	}

	if len(describeOutput.Tasks) == 0 || len(describeOutput.Tasks[0].Containers) == 0 {
		return fmt.Errorf("migration task failed to start")
	}

	exitCode := describeOutput.Tasks[0].Containers[0].ExitCode
	if exitCode == nil {
		return fmt.Errorf("migration task failed to start (no exit code)")
	}

	if *exitCode != 0 {
		logGroup := fmt.Sprintf("/ecs/%s", opts.taskDef)
		printInfo(fmt.Sprintf("Fetching recent logs from %s...", logGroup))
		fetchLogs(ctx, cfg, logGroup)
		return fmt.Errorf("migration task failed with exit code %d", *exitCode)
	}

	printSuccess("Migration task completed successfully")
	return nil
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
