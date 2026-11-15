package migrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/pflag"
)

// fill sets every settable field of v to a non-zero value of its type.
func fill(v reflect.Value) {
	for i := range v.NumField() {
		field := v.Field(i)
		if !field.CanSet() {
			continue
		}
		switch field.Kind() {
		case reflect.Pointer:
			elem := reflect.New(field.Type().Elem())
			if elem.Elem().Kind() == reflect.String {
				elem.Elem().SetString("x")
			}
			field.Set(elem)
		case reflect.Slice:
			field.Set(reflect.MakeSlice(field.Type(), 1, 1))
		case reflect.String:
			field.SetString("x")
		case reflect.Int32, reflect.Int64:
			field.SetInt(1)
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Map:
			field.Set(reflect.MakeMap(field.Type()))
		}
	}
}

func TestRegisterInputCopiesEverySourceField(t *testing.T) {
	var td ecstypes.TaskDefinition
	fill(reflect.ValueOf(&td).Elem())
	input := reflect.ValueOf(registerInputFrom(&td)).Elem()
	source := reflect.ValueOf(td)
	for i := range input.NumField() {
		name := input.Type().Field(i).Name
		if !input.Type().Field(i).IsExported() {
			continue
		}
		got := input.Field(i)
		if name == "Tags" {
			if !got.IsZero() {
				t.Errorf("Tags = %v, want none: tagging needs ecs:TagResource", got.Interface())
			}
			continue
		}
		want := source.FieldByName(name)
		if !want.IsValid() {
			t.Errorf("RegisterTaskDefinitionInput.%s has no TaskDefinition field to copy from", name)
			continue
		}
		if got.IsZero() || !reflect.DeepEqual(got.Interface(), want.Interface()) {
			t.Errorf("RegisterTaskDefinitionInput.%s was not copied from the source", name)
		}
	}
}

func TestLogTargetReadsAwslogsOptions(t *testing.T) {
	containers := []ecstypes.ContainerDefinition{
		{Name: aws.String("sidecar")},
		{Name: aws.String("migrate"), LogConfiguration: &ecstypes.LogConfiguration{
			LogDriver: ecstypes.LogDriverAwslogs,
			Options:   map[string]string{"awslogs-group": "/app/api", "awslogs-stream-prefix": "ecs", "awslogs-region": "eu-west-1"},
		}},
	}
	target := logTargetFor(&containers[1])
	if target == nil || target.group != "/app/api" || target.region != "eu-west-1" {
		t.Fatalf("logTargetFor() = %+v, want the awslogs group and region", target)
	}
	if got := target.stream("migrate", "arn:aws:ecs:eu-west-1:123:task/cluster/abc123"); got != "ecs/migrate/abc123" {
		t.Errorf("stream() = %q, want ecs/migrate/abc123", got)
	}
	if logTargetFor(&containers[0]) != nil {
		t.Error("logTargetFor(no log configuration) != nil")
	}
	delete(containers[1].LogConfiguration.Options, "awslogs-stream-prefix")
	if logTargetFor(&containers[1]) != nil {
		t.Error("logTargetFor(no stream prefix) != nil; awslogs names such streams by task id alone")
	}
}

// fakeLogs serves pages of events for one stream and fails for any other, or before ready reports
// the stream exists.
type fakeLogs struct {
	stream string
	pages  [][]string
	calls  int
	ready  func() bool // false fails the read, as a read before the task started would
	hidden func() bool // true reads as a stream CloudWatch has not created yet
}

func (f *fakeLogs) GetLogEvents(ctx context.Context, input *cloudwatchlogs.GetLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	if aws.ToString(input.LogStreamName) != f.stream {
		return nil, errors.New("read another task's stream")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("operation error CloudWatch Logs: GetLogEvents: %w", err)
	}
	if f.ready != nil && !f.ready() {
		return nil, errors.New("read the stream before the task started")
	}
	if f.hidden != nil && f.hidden() {
		return nil, &logstypes.ResourceNotFoundException{Message: aws.String("The specified log stream does not exist.")}
	}
	page := 0
	if input.NextToken != nil {
		page = int(aws.ToString(input.NextToken)[0] - '0')
	}
	if f.calls++; f.calls > 10 {
		return nil, errors.New("drain kept reading after the stream ran dry")
	}
	output := &cloudwatchlogs.GetLogEventsOutput{NextForwardToken: aws.String(string(rune('0' + min(page+1, len(f.pages)))))}
	if page < len(f.pages) {
		for _, message := range f.pages[page] {
			output.Events = append(output.Events, logstypes.OutputLogEvent{Message: aws.String(message)})
		}
	}
	return output, nil
}

// fakeECS answers with a fixed sequence of task statuses and records what was registered and run.
type fakeECS struct {
	statuses   []string
	described  int
	registered *ecs.RegisterTaskDefinitionInput
	ran        bool
	polled     chan struct{} // closed on the first DescribeTasks, when set
	fail       map[int]bool  // the DescribeTasks calls, counting from 1, that fail
	missing    bool          // DescribeTasks finds no task
	exitCode   int32         // the migration's exit code once stopped
}

func (f *fakeECS) DescribeTaskDefinition(context.Context, *ecs.DescribeTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	return &ecs.DescribeTaskDefinitionOutput{Tags: []ecstypes.Tag{{Key: aws.String("team"), Value: aws.String("data")}}, TaskDefinition: &ecstypes.TaskDefinition{
		Family:            aws.String("api"),
		TaskDefinitionArn: aws.String("arn:aws:ecs:us-east-1:123:task-definition/api:7"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("migrate"), Image: aws.String("api:old"),
			LogConfiguration: &ecstypes.LogConfiguration{LogDriver: ecstypes.LogDriverAwslogs,
				Options: map[string]string{"awslogs-group": "/app/api", "awslogs-stream-prefix": "ecs"}},
		}},
	}}, nil
}

func (f *fakeECS) RegisterTaskDefinition(_ context.Context, input *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	f.registered = input
	return &ecs.RegisterTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{TaskDefinitionArn: aws.String("arn:aws:ecs:us-east-1:123:task-definition/api:8")}}, nil
}

func (f *fakeECS) RunTask(context.Context, *ecs.RunTaskInput, ...func(*ecs.Options)) (*ecs.RunTaskOutput, error) {
	f.ran = true
	return &ecs.RunTaskOutput{Tasks: []ecstypes.Task{{TaskArn: aws.String("arn:aws:ecs:us-east-1:123:task/cluster/abc123")}}}, nil
}

func (f *fakeECS) DescribeTasks(ctx context.Context, _ *ecs.DescribeTasksInput, _ ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("operation error ECS: DescribeTasks: %w", err)
	}
	status := f.statuses[min(f.described, len(f.statuses)-1)]
	if f.described == 0 && f.polled != nil {
		close(f.polled)
	}
	f.described++
	if f.fail[f.described] {
		return nil, errors.New("RequestExpired")
	}
	if f.missing {
		return &ecs.DescribeTasksOutput{Failures: []ecstypes.Failure{{Reason: aws.String("MISSING")}}}, nil
	}
	task := ecstypes.Task{TaskArn: aws.String("arn:aws:ecs:us-east-1:123:task/cluster/abc123"), LastStatus: aws.String(status)}
	if status != "PROVISIONING" {
		task.StartedAt = aws.Time(time.Unix(1_700_000_000, 0))
	}
	if status == "STOPPED" || status == "DEPROVISIONING" {
		task.Containers = []ecstypes.Container{{Name: aws.String("migrate"), ExitCode: aws.Int32(f.exitCode)}}
	}
	return &ecs.DescribeTasksOutput{Tasks: []ecstypes.Task{task}}, nil
}

// fakeClients serves e and l in place of AWS.
func fakeClients(e *fakeECS, l logsAPI) clients {
	return func(context.Context, string) (ecsAPI, func(string) logsAPI, error) {
		return e, func(string) logsAPI { return l }, nil
	}
}

func testOptions() *options {
	return &options{cluster: "main", taskDef: "api:7", subnets: "s1", securityGroups: "g1", container: "migrate",
		imageURI: "api:new", region: "us-east-1", waitTimeout: time.Minute}
}

func TestRunMigrationStreamsOnlyThisTasksLogs(t *testing.T) {
	ecsClient := &fakeECS{statuses: []string{"PROVISIONING", "RUNNING", "STOPPED"}}
	logs := &fakeLogs{stream: "ecs/migrate/abc123", pages: [][]string{{"applying 001"}, {"applying 002", "\x1b[32mdone\x1b[0m"}},
		ready: func() bool { return ecsClient.described > 1 }}
	var out, errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &errOut, ui.Options{})
	if err := runMigrationEvery(context.Background(), u, testOptions(), fakeClients(ecsClient, logs), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "applying 001\napplying 002\ndone\n" {
		t.Errorf("stdout = %q, want this task's log lines in order", got)
	}
	if strings.Contains(errOut.String(), "warning") {
		t.Errorf("stderr = %q, want no warnings", errOut.String())
	}
	if len(ecsClient.registered.Tags) != 0 {
		t.Errorf("registered tags %+v, want none", ecsClient.registered.Tags)
	}
	if ecsClient.registered == nil || aws.ToString(ecsClient.registered.Family) != "api" ||
		aws.ToString(ecsClient.registered.ContainerDefinitions[0].Image) != "api:new" {
		t.Errorf("registered %+v, want family api with the new image", ecsClient.registered)
	}
}

func TestDryRunRegistersNothing(t *testing.T) {
	ecsClient := &fakeECS{statuses: []string{"STOPPED"}}
	var out bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{})
	opts := testOptions()
	opts.dryRun = true
	err := runMigrationEvery(context.Background(), u, opts, fakeClients(ecsClient, &fakeLogs{stream: "ecs/migrate/abc123"}), time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if ecsClient.registered != nil || ecsClient.ran {
		t.Error("dry run registered or ran a task")
	}
	if !strings.Contains(out.String(), "container migrate image: api:old -> api:new") {
		t.Errorf("stdout = %q, want the image change", out.String())
	}
}

func TestInterruptNamesTheRunningTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ecsClient := &fakeECS{statuses: []string{"RUNNING"}, polled: make(chan struct{})}
	var errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &bytes.Buffer{}, &errOut, ui.Options{})
	go func() {
		<-ecsClient.polled
		cancel()
	}()
	err := runMigrationEvery(ctx, u, testOptions(), fakeClients(ecsClient, &fakeLogs{stream: "ecs/migrate/abc123"}), 5*time.Millisecond)
	if !errors.Is(err, context.Canceled) || !strings.Contains(errOut.String(), "task arn:aws:ecs:us-east-1:123:task/cluster/abc123 may still be running") {
		t.Errorf("err = %v, stderr = %q; want the running task's ARN", err, errOut.String())
	}
}

// missingStream fails every read the way CloudWatch does before the task creates its stream.
type missingStream struct{}

func (missingStream) GetLogEvents(context.Context, *cloudwatchlogs.GetLogEventsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	return nil, &logstypes.ResourceNotFoundException{Message: aws.String("The specified log stream does not exist.")}
}

func TestDrainReadsAMissingStreamAsEmpty(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{})
	logs := &logStream{client: missingStream{}, group: "/app/api", stream: "ecs/migrate/abc123"}
	if err := logs.drain(context.Background(), u); err != nil || out.Len() != 0 {
		t.Errorf("drain() = %v, stdout %q; want no error and no output", err, out.String())
	}
}

func TestRunMigrationRejectsAnUnknownContainer(t *testing.T) {
	ecsClient := &fakeECS{statuses: []string{"STOPPED"}}
	opts := testOptions()
	opts.container = "migrator"
	u := ui.NewWithStreams(nil, &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{})
	err := runMigrationEvery(context.Background(), u, opts, fakeClients(ecsClient, &fakeLogs{}), time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "has no migrator container") || ecsClient.registered != nil {
		t.Errorf("err = %v, registered = %v; want the missing container named before registering", err, ecsClient.registered != nil)
	}
}

func TestEnvVariableFollowsTheFlagName(t *testing.T) {
	if got := envVariable("security-groups"); got != "SECURITY_GROUPS" {
		t.Errorf("envVariable(security-groups) = %q, want SECURITY_GROUPS", got)
	}
}

func TestLegacyEnvFailsUnlessReplaced(t *testing.T) {
	t.Setenv("CLUSTER", "main")
	t.Setenv("REGION", "eu-west-1")
	t.Setenv("DEVEX_REGION", "eu-west-1")
	t.Setenv("SUBNETS", "s1")
	t.Setenv("CONTAINER", "") // empty counts as unset, as it always has
	cmd := Command()
	if err := cmd.ParseFlags([]string{"--subnets", "s2"}); err != nil {
		t.Fatal(err)
	}
	err := checkLegacyEnv(cmd, []string{"cluster", "region", "subnets", "container"})
	if err == nil || !strings.Contains(err.Error(), "CLUSTER is set, but migrate reads DEVEX_CLUSTER now") ||
		strings.Contains(err.Error(), "REGION") || strings.Contains(err.Error(), "SUBNETS") || strings.Contains(err.Error(), "CONTAINER") {
		t.Errorf("checkLegacyEnv() = %v, want CLUSTER alone: DEVEX_REGION and --subnets replace the others", err)
	}
}

func TestFailedCheckSettlesAndReadsTheLastLines(t *testing.T) {
	// Three failed checks in a row settle on call 5, the only one that sees the exited migration.
	ecsClient := &fakeECS{statuses: []string{"RUNNING", "RUNNING", "RUNNING", "RUNNING", "DEPROVISIONING", "RUNNING"},
		fail: map[int]bool{2: true, 3: true, 4: true}}
	logs := &fakeLogs{stream: "ecs/migrate/abc123", pages: [][]string{{"error: relation exists"}},
		ready: func() bool { return ecsClient.described > 4 }}
	var out, errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &errOut, ui.Options{})
	opts := testOptions()
	opts.waitTimeout = 100 * time.Millisecond
	if err := runMigrationEvery(context.Background(), u, opts, fakeClients(ecsClient, logs), time.Millisecond); err != nil {
		t.Fatalf("runMigrationEvery() = %v, want the settled task's result", err)
	}
	if out.String() != "error: relation exists\n" {
		t.Errorf("stdout = %q, want the lines read after settling", out.String())
	}
	if !strings.HasSuffix(strings.TrimSuffix(errOut.String(), "✓ Migration task completed\n"), "Migration task DEPROVISIONING\n") {
		t.Errorf("stderr = %q, want the settled status reported", errOut.String())
	}
}

func TestWatchFailsOnceECSStopsFindingTheTask(t *testing.T) {
	watch := &taskWatch{client: &fakeECS{statuses: []string{"RUNNING"}, missing: true},
		input: &ecs.DescribeTasksInput{Tasks: []string{"arn:task"}}, timeout: time.Second, poll: time.Millisecond, missing: 5 * time.Millisecond}
	_, err := watch.run(context.Background(), ui.NewWithStreams(nil, &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{}))
	if err == nil || !strings.Contains(err.Error(), "ECS no longer finds task arn:task") || strings.Contains(err.Error(), "did not stop within") {
		t.Errorf("run() = %v, want the task named with ECS's reason", err)
	}
}

func TestPrintableStripsTerminalControl(t *testing.T) {
	got := printable("a\x1b]52;c;ZXZpbA==\x07b \x1b[31mred\x1b[0m\rover\ttab\x08\nnext\u009b\u202e\u2028\u2029 a\u200db")
	if got != "ab redover\ttab\nnext a\u200db" {
		t.Errorf("printable() = %q, want escapes and control characters gone, tab and newline kept", got)
	}
}

func TestLogFailuresWarnOnce(t *testing.T) {
	ecsClient := &fakeECS{statuses: []string{"RUNNING", "RUNNING", "RUNNING", "STOPPED"}}
	logs := &fakeLogs{stream: "ecs/migrate/abc123", ready: func() bool { return false }}
	var errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &bytes.Buffer{}, &errOut, ui.Options{})
	if err := runMigrationEvery(context.Background(), u, testOptions(), fakeClients(ecsClient, logs), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(errOut.String(), "warning: read logs"); got != 1 || strings.Contains(errOut.String(), "found no log stream") {
		t.Errorf("stderr = %q, want one read warning and no missing-stream claim", errOut.String())
	}
}

func TestWatchReturnsCancellationAsIs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watch := &taskWatch{client: &fakeECS{statuses: []string{"RUNNING"}}, input: &ecs.DescribeTasksInput{Tasks: []string{"arn:task"}},
		timeout: time.Minute, poll: time.Millisecond, missing: time.Minute}
	_, err := watch.run(ctx, ui.NewWithStreams(nil, &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{}))
	if err != context.Canceled { // nothing may wrap it
		t.Errorf("run() = %v, want context.Canceled itself rather than a settle report", err)
	}
}

func TestTransientCheckFailureKeepsWatching(t *testing.T) {
	// Four failures, never three in a row: a success in between resets the count.
	ecsClient := &fakeECS{statuses: []string{"RUNNING", "RUNNING", "RUNNING", "RUNNING", "RUNNING", "RUNNING", "STOPPED"},
		fail: map[int]bool{2: true, 3: true, 5: true, 6: true}}
	var errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &bytes.Buffer{}, &errOut, ui.Options{})
	if err := runMigrationEvery(context.Background(), u, testOptions(), fakeClients(ecsClient, &fakeLogs{stream: "ecs/migrate/abc123"}), time.Millisecond); err != nil {
		t.Errorf("runMigrationEvery() = %v, want the watch to ride out failed checks that never run three in a row", err)
	}
	if got := strings.Count(errOut.String(), "Migration task RUNNING\n"); got != 1 || !strings.Contains(errOut.String(), "Migration task STOPPED\n") {
		t.Errorf("stderr = %q, want each status change once", errOut.String())
	}
}

func TestFailedMigrationRereadsItsLastLines(t *testing.T) {
	ecsClient := &fakeECS{statuses: []string{"RUNNING", "STOPPED"}, exitCode: 1}
	reads := 0
	logs := &fakeLogs{stream: "ecs/migrate/abc123", pages: [][]string{{"ERROR: relation exists"}},
		hidden: func() bool { reads++; return reads <= 2 }} // ingested only after the task stopped
	var out, errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &errOut, ui.Options{})
	err := runMigrationEvery(context.Background(), u, testOptions(), fakeClients(ecsClient, logs), time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "exit code 1") || out.String() != "ERROR: relation exists\n" {
		t.Errorf("err = %v, stdout %q; want the failure and its last line", err, out.String())
	}
	if strings.Contains(errOut.String(), "found no log stream") {
		t.Errorf("stderr = %q, want no missing-stream warning", errOut.String())
	}
}

func TestWarnsWhenTheStreamNeverAppears(t *testing.T) {
	ecsClient := &fakeECS{statuses: []string{"RUNNING", "STOPPED"}}
	logs := &fakeLogs{stream: "ecs/migrate/abc123", hidden: func() bool { return true }}
	var errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &bytes.Buffer{}, &errOut, ui.Options{})
	if err := runMigrationEvery(context.Background(), u, testOptions(), fakeClients(ecsClient, logs), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "found no log stream ecs/migrate/abc123 in group /app/api") {
		t.Errorf("stderr = %q, want the missing stream named", errOut.String())
	}
}

func TestCancelledReadIsNoLogFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var errOut bytes.Buffer
	watch := &taskWatch{logs: &logStream{client: &fakeLogs{stream: "s"}, group: "g", stream: "s"}}
	watch.drain(ctx, ui.NewWithStreams(nil, &bytes.Buffer{}, &errOut, ui.Options{}))
	if errOut.Len() != 0 || watch.logWarned {
		t.Errorf("stderr = %q, want no warning for a read Ctrl-C cut short", errOut.String())
	}
}

func TestCommandRejectsANonPositiveWaitTimeout(t *testing.T) {
	cmd := Command()
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		t.Setenv(envVariable(flag.Name), "") // the host's own REGION and the like must not decide the test
	})
	t.Setenv("AWS_PROFILE", "devex-test-no-such-profile") // fail offline should the check ever let it through
	cmd.SetArgs([]string{"--cluster", "c", "--task-def", "t", "--subnets", "s", "--security-groups", "g",
		"--container", "migrate", "--image-uri", "i", "--wait-timeout", "0s"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--wait-timeout must be positive") {
		t.Errorf("Execute() = %v, want the timeout rejected before any AWS call", err)
	}
}

func TestSettledFailureRereadsItsLastLines(t *testing.T) {
	// Three failed checks settle on call 5, which finds the migration exited 1.
	ecsClient := &fakeECS{statuses: []string{"RUNNING", "RUNNING", "RUNNING", "RUNNING", "DEPROVISIONING"},
		fail: map[int]bool{2: true, 3: true, 4: true}, exitCode: 1}
	settledReads := 0
	logs := &fakeLogs{stream: "ecs/migrate/abc123", pages: [][]string{{"ERROR: relation exists"}},
		hidden: func() bool { // ingested only after the settling read
			if ecsClient.described <= 4 {
				return true
			}
			settledReads++
			return settledReads == 1
		}}
	var out bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{})
	err := runMigrationEvery(context.Background(), u, testOptions(), fakeClients(ecsClient, logs), time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "exit code 1") || out.String() != "ERROR: relation exists\n" {
		t.Errorf("err = %v, stdout %q; want the failure and its last line", err, out.String())
	}
}

func TestEmptyAnswersResetFailedChecks(t *testing.T) {
	// err, empty, err, empty, err: never three failures in a row, so only the timeout ends it.
	watch := &taskWatch{client: &fakeECS{statuses: []string{"RUNNING"}, missing: true, fail: map[int]bool{1: true, 3: true, 5: true}},
		input: &ecs.DescribeTasksInput{Tasks: []string{"arn:task"}}, timeout: 50 * time.Millisecond, poll: time.Millisecond, missing: time.Minute}
	_, err := watch.run(context.Background(), ui.NewWithStreams(nil, &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{}))
	if err == nil || !strings.Contains(err.Error(), "did not stop within") {
		t.Errorf("run() = %v, want the timeout to end the watch, not the failed checks", err)
	}
}
