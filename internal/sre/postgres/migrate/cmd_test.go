package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestResolveLaunchType(t *testing.T) {
	cases := []struct {
		name  string
		flag  string
		valid bool
		want  ecstypes.LaunchType
	}{
		{name: "fargate", flag: "FARGATE", valid: true, want: ecstypes.LaunchTypeFargate},
		{name: "ec2", flag: "EC2", valid: true, want: ecstypes.LaunchTypeEc2},
		{name: "lowercase is normalized", flag: "ec2", valid: true, want: ecstypes.LaunchTypeEc2},
		{name: "unknown value", flag: "SERVERLESS"},
		{name: "empty value", flag: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &options{launchType: tc.flag}
			got, err := opts.resolveLaunchType()

			if !tc.valid {
				if err == nil {
					t.Fatalf("resolveLaunchType(%q) = %q, want an error", tc.flag, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveLaunchType(%q): %v", tc.flag, err)
			}
			if got != tc.want {
				t.Fatalf("resolveLaunchType(%q) = %q, want %q", tc.flag, got, tc.want)
			}
		})
	}
}

func TestPlacementDefaultsToFargate(t *testing.T) {
	opts := &options{}

	launchType, strategy, err := opts.placement()
	if err != nil {
		t.Fatalf("placement(): %v", err)
	}
	if launchType != ecstypes.LaunchTypeFargate || strategy != nil {
		t.Fatalf("placement() = %q, %+v, want FARGATE and no strategy", launchType, strategy)
	}
}

func TestCommandReadsLaunchTypeFromEnvironment(t *testing.T) {
	t.Setenv("LAUNCH_TYPE", "EC2")

	cmd := Command()
	flag := cmd.Flags().Lookup("launch-type")
	if flag == nil {
		t.Fatal("--launch-type flag is not registered")
	}
	if flag.DefValue != string(ecstypes.LaunchTypeEc2) {
		t.Fatalf("--launch-type default = %q, want %q", flag.DefValue, ecstypes.LaunchTypeEc2)
	}
}

func TestResolveLaunchTypeErrorListsValidValues(t *testing.T) {
	opts := &options{launchType: "SERVERLESS"}

	_, err := opts.resolveLaunchType()
	if err == nil {
		t.Fatal("resolveLaunchType() accepted an invalid launch type")
	}
	if !strings.Contains(err.Error(), string(ecstypes.LaunchTypeEc2)) {
		t.Fatalf("error does not name the valid values: %v", err)
	}
}

func TestPlacementUsesTheNamedCapacityProvider(t *testing.T) {
	opts := &options{capacityProvider: "staging-ecs-cluster"}

	launchType, strategy, err := opts.placement()
	if err != nil {
		t.Fatalf("placement(): %v", err)
	}
	if launchType != "" {
		t.Fatalf("placement() launch type = %q, want none alongside a capacity provider", launchType)
	}
	if len(strategy) != 1 || *strategy[0].CapacityProvider != "staging-ecs-cluster" || strategy[0].Weight != 1 {
		t.Fatalf("placement() strategy = %+v, want staging-ecs-cluster with weight 1", strategy)
	}
}

func TestPlacementFallsBackToTheLaunchType(t *testing.T) {
	opts := &options{launchType: "ec2"}

	launchType, strategy, err := opts.placement()
	if err != nil {
		t.Fatalf("placement(): %v", err)
	}
	if launchType != ecstypes.LaunchTypeEc2 || strategy != nil {
		t.Fatalf("placement() = %q, %+v, want EC2 and no strategy", launchType, strategy)
	}
}

func TestPlacementRejectsBothACapacityProviderAndALaunchType(t *testing.T) {
	opts := &options{capacityProvider: "staging-ecs-cluster", launchType: "EC2"}

	_, _, err := opts.placement()
	if err == nil {
		t.Fatal("placement() accepted both --capacity-provider and --launch-type")
	}
	if !strings.Contains(err.Error(), "CAPACITY_PROVIDER") || !strings.Contains(err.Error(), "LAUNCH_TYPE") {
		t.Fatalf("error does not name the environment variables that can set either: %v", err)
	}
}

func TestMigrationExitCodeReadsTheNamedContainer(t *testing.T) {
	task := ecstypes.Task{Containers: []ecstypes.Container{
		{Name: aws.String("log-router"), ExitCode: aws.Int32(0)},
		{Name: aws.String("migrator"), ExitCode: aws.Int32(1)},
	}}

	exitCode, err := migrationExitCode(task, "migrator")
	if err != nil {
		t.Fatalf("migrationExitCode(): %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("migrationExitCode() = %d, want the migrator's 1", exitCode)
	}
}

func TestMigrationExitCodeReportsWhyATaskNeverRan(t *testing.T) {
	task := ecstypes.Task{
		StopCode:      ecstypes.TaskStopCodeTaskFailedToStart,
		StoppedReason: aws.String("Capacity is unavailable at this time"),
		Containers:    []ecstypes.Container{{Name: aws.String("migrator")}},
	}

	_, err := migrationExitCode(task, "migrator")
	if err == nil || !strings.Contains(err.Error(), "before migrator ran: TaskFailedToStart: Capacity is unavailable at this time") {
		t.Fatalf("migrationExitCode() error = %v, want the stop code and reason", err)
	}
}

func TestMigrationExitCodeFlagsAHostLostMidRun(t *testing.T) {
	task := ecstypes.Task{
		StartedAt:     aws.Time(time.Unix(1_700_000_000, 0)),
		StoppedReason: aws.String("Host EC2 instance terminated"),
		Containers:    []ecstypes.Container{{Name: aws.String("migrator")}},
	}

	_, err := migrationExitCode(task, "migrator")
	if err == nil || !strings.Contains(err.Error(), "mid-run") || strings.Contains(err.Error(), ": :") {
		t.Fatalf("migrationExitCode() error = %v, want a mid-run failure without empty segments", err)
	}
}

func TestMigrationExitCodeRejectsAMissingContainer(t *testing.T) {
	task := ecstypes.Task{Containers: []ecstypes.Container{{Name: aws.String("app"), ExitCode: aws.Int32(0)}}}

	if _, err := migrationExitCode(task, "migrator"); err == nil {
		t.Fatal("migrationExitCode() accepted a task without the migrator container")
	}
}

// describeTasks is a DescribeTasks-only ECS client that returns a canned answer.
type describeTasks struct {
	output *ecs.DescribeTasksOutput
	err    error
}

func (d describeTasks) DescribeTasks(context.Context, *ecs.DescribeTasksInput, ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	return d.output, d.err
}

// taskIn returns a task in status whose migrator exited with exitCode, if set, beside a sidecar that
// already exited.
func taskIn(status string, exitCode *int32) describeTasks {
	return describeTasks{output: &ecs.DescribeTasksOutput{Tasks: []ecstypes.Task{{
		TaskArn:    aws.String("arn:task"),
		LastStatus: aws.String(status),
		Containers: []ecstypes.Container{
			{Name: aws.String("log-router"), ExitCode: aws.Int32(0)},
			{Name: aws.String("migrator"), ExitCode: exitCode},
		},
	}}}}
}

func settle(t *testing.T, client ecs.DescribeTasksAPIClient) (ecstypes.Task, error) {
	t.Helper()

	return settleAfterWait(t.Context(), client, &ecs.DescribeTasksInput{Cluster: aws.String("c"), Tasks: []string{"arn:task"}}, "migrator")
}

func TestSettleAfterWaitReportsAMigrationThatAlreadyExited(t *testing.T) {
	for _, status := range []string{"STOPPED", "DEPROVISIONING"} {
		t.Run(status, func(t *testing.T) {
			task, err := settle(t, taskIn(status, aws.Int32(0)))
			if err != nil {
				t.Fatalf("settleAfterWait(): %v", err)
			}
			if exitCode, err := migrationExitCode(task, "migrator"); err != nil || exitCode != 0 {
				t.Fatalf("migrationExitCode() = %d, %v, want the exited migrator's 0", exitCode, err)
			}
		})
	}
}

func TestSettleAfterWaitReportsATaskStoppedBeforeItRan(t *testing.T) {
	task, err := settle(t, taskIn("STOPPED", nil))
	if err != nil {
		t.Fatalf("settleAfterWait(): %v", err)
	}
	if _, err := migrationExitCode(task, "migrator"); err == nil || !strings.Contains(err.Error(), "before migrator ran") {
		t.Fatalf("migrationExitCode() error = %v, want the never-ran report", err)
	}
}

func TestSettleAfterWaitReportsATaskECSAbandonedBeforeItRan(t *testing.T) {
	client := taskIn("DEPROVISIONING", nil)
	client.output.Tasks[0].DesiredStatus = aws.String("STOPPED")
	client.output.Tasks[0].StoppedReason = aws.String("Capacity is unavailable at this time")

	task, err := settle(t, client)
	if err != nil {
		t.Fatalf("settleAfterWait(): %v", err)
	}
	if _, err := migrationExitCode(task, "migrator"); err == nil || !strings.Contains(err.Error(), "before migrator ran") {
		t.Fatalf("migrationExitCode() error = %v, want the never-ran report", err)
	}
}

// Only the migrator's exit counts: the sidecar in taskIn has exited, the migration has not.
func TestSettleAfterWaitNamesAnUnfinishedTask(t *testing.T) {
	for _, status := range []string{"PROVISIONING", "RUNNING"} {
		t.Run(status, func(t *testing.T) {
			_, err := settle(t, taskIn(status, nil))
			if err == nil || !strings.Contains(err.Error(), "still "+status) || !strings.Contains(err.Error(), "arn:task") {
				t.Fatalf("settleAfterWait() error = %v, want the task and its %s status", err, status)
			}
		})
	}
}

func TestSettleAfterWaitReportsAFailedDescribe(t *testing.T) {
	_, err := settle(t, describeTasks{err: errors.New("ThrottlingException")})
	if err == nil || !strings.Contains(err.Error(), "ThrottlingException") {
		t.Fatalf("settleAfterWait() error = %v, want the describe failure", err)
	}
}

func TestSettleAfterWaitReportsAMissingTask(t *testing.T) {
	_, err := settle(t, describeTasks{output: &ecs.DescribeTasksOutput{
		Failures: []ecstypes.Failure{{Arn: aws.String("arn:task"), Reason: aws.String("MISSING")}},
	}})
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("settleAfterWait() error = %v, want ECS's failure reason", err)
	}
}

func TestMigrationExitCodeFallsBackWhenECSGivesNoReason(t *testing.T) {
	task := ecstypes.Task{Containers: []ecstypes.Container{{Name: aws.String("migrator")}}}

	_, err := migrationExitCode(task, "migrator")
	if err == nil || !strings.HasSuffix(err.Error(), "no reason given") {
		t.Fatalf("migrationExitCode() error = %v, want a fallback reason", err)
	}
}

func TestOverrideImageSetsTheNamedContainer(t *testing.T) {
	containers := []ecstypes.ContainerDefinition{
		{Name: aws.String("log-router"), Image: aws.String("fluent:1")},
		{Name: aws.String("migrator"), Image: aws.String("migration:v1")},
	}

	if err := overrideImage(containers, "migrator", "migration:v2"); err != nil {
		t.Fatalf("overrideImage(): %v", err)
	}
	if aws.ToString(containers[1].Image) != "migration:v2" || aws.ToString(containers[0].Image) != "fluent:1" {
		t.Fatalf("overrideImage() images = %q, %q, want only the migrator changed", aws.ToString(containers[0].Image), aws.ToString(containers[1].Image))
	}
}

func TestOverrideImageRejectsAnUnknownContainer(t *testing.T) {
	containers := []ecstypes.ContainerDefinition{{Name: aws.String("migrator"), Image: aws.String("migration:v1")}}

	if err := overrideImage(containers, "migrate", "migration:v2"); err == nil {
		t.Fatal("overrideImage() accepted a container name the task definition lacks")
	}
}
