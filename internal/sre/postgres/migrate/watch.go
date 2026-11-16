package migrate

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/charmbracelet/x/ansi"
	"github.com/project-init/devex/internal/ui"
)

const defaultPollInterval = 5 * time.Second

// logStream reads one task's CloudWatch log stream forward from where it last stopped.
type logStream struct {
	client   logsAPI
	group    string
	stream   string
	token    *string
	found    bool // a read found the stream
	notFound bool // a read reported the stream missing
}

// drain prints every event written since the last call, following pages until none are new. A
// stream the task has not created yet reads as empty.
func (l *logStream) drain(ctx context.Context, u *ui.UI) error {
	for {
		output, err := l.client.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
			LogGroupName:  &l.group,
			LogStreamName: &l.stream,
			StartFromHead: aws.Bool(true),
			NextToken:     l.token,
		})
		if err != nil {
			var notFound *logstypes.ResourceNotFoundException
			if errors.As(err, &notFound) {
				l.notFound = true
				return nil
			}
			return fmt.Errorf("read logs %s: %w", l.stream, err)
		}
		l.found = true
		for _, event := range output.Events {
			u.Printf("%s\n", printable(aws.ToString(event.Message)))
		}
		// The forward token repeats once the stream has nothing newer.
		if output.NextForwardToken == nil || aws.ToString(output.NextForwardToken) == aws.ToString(l.token) {
			return nil
		}
		l.token = output.NextForwardToken
	}
}

// printable strips terminal escape sequences, control characters other than tab and newline, bidi
// controls, and line separators from container output, so a log line can't move the cursor, reorder
// text, or rewrite the operator's terminal. Joiners stay, since scripts and emoji need them.
func printable(text string) string {
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\t' && r != '\n') || unicode.In(r, unicode.Bidi_Control, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

// taskWatch polls one task until it stops, streaming its logs.
type taskWatch struct {
	client    ecs.DescribeTasksAPIClient
	logs      *logStream // nil when the container's logs can't be streamed
	input     *ecs.DescribeTasksInput
	container string
	timeout   time.Duration
	poll      time.Duration
	missing   time.Duration // how long ECS may fail to find the task, as it can right after RunTask
	logWarned bool
}

// maxDescribeFailures is how many status checks in a row may fail before the watch settles.
const maxDescribeFailures = 3

// run shows the task's status and elapsed time while it runs, and returns the stopped task. When the
// timeout passes or a status check fails first, it settles on what ECS still knows, leaving the task
// alone.
func (w *taskWatch) run(ctx context.Context, u *ui.UI) (ecstypes.Task, error) {
	start := time.Now()
	var task ecstypes.Task
	err := u.SpinStatus(ctx, "Waiting for the migration task", func(ctx context.Context, setTitle func(string)) error {
		reported := ""
		// report shows the task's status. Off a terminal the title never redraws, so a log or CI run
		// gets each status change as a line instead.
		report := func() string {
			status := cmp.Or(aws.ToString(task.LastStatus), "starting")
			setTitle(fmt.Sprintf("Migration task %s · %s", status, time.Since(start).Round(time.Second)))
			if !u.Live() && status != reported {
				u.Status("Migration task %s", status)
				reported = status
			}
			return status
		}
		settle := func(why string) error {
			settled, err := settleAfterWait(ctx, w.client, w.input, w.container)
			if err != nil {
				return fmt.Errorf("%s: %w", why, err)
			}
			task = settled
			report()
			w.finish(ctx, u, task)
			return nil
		}
		deadline := time.After(w.timeout)
		failures := 0
		for {
			described, err := w.client.DescribeTasks(ctx, w.input)
			switch {
			case err != nil && ctx.Err() != nil:
				return ctx.Err()
			case err != nil:
				// A throttled or dropped check can succeed on the next poll.
				if failures++; failures >= maxDescribeFailures {
					return settle(fmt.Sprintf("describe task: %v", err))
				}
			case len(described.Tasks) > 0:
				failures = 0
				task = described.Tasks[0]
			case time.Since(start) > w.missing:
				data, _ := json.Marshal(described.Failures)
				return fmt.Errorf("ECS no longer finds task %s: %s", strings.Join(w.input.Tasks, ", "), data)
			default:
				failures = 0
			}
			if report() == string(ecstypes.DesiredStatusStopped) {
				w.finish(ctx, u, task)
				return nil
			}
			// awslogs creates the stream when the container starts, so reading earlier finds nothing.
			if task.StartedAt != nil {
				w.drain(ctx, u)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline:
				return settle(fmt.Sprintf("the task did not stop within %s", w.timeout))
			case <-time.After(w.poll):
			}
		}
	})
	return task, err
}

// drain prints the task's new log lines. A failed read warns once and leaves streaming on, since a
// throttled read can succeed on the next poll.
func (w *taskWatch) drain(ctx context.Context, u *ui.UI) {
	if w.logs == nil {
		return
	}
	// A read Ctrl-C cut short is no log failure.
	if err := w.logs.drain(ctx, u); err != nil && ctx.Err() == nil && !w.logWarned {
		u.Warn("%v", err)
		w.logWarned = true
	}
}

// finish reads a stopped task's last log lines. CloudWatch can take a moment to ingest a failing
// migration's final lines, often its error, so a failed task gets one more read a poll later.
func (w *taskWatch) finish(ctx context.Context, u *ui.UI, task ecstypes.Task) {
	if w.logs == nil || task.StartedAt == nil {
		return
	}
	w.drain(ctx, u)
	if exitCode, err := migrationExitCode(task, w.container); err != nil || exitCode != 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.poll):
		}
		w.drain(ctx, u)
	}
	if w.logs.notFound && !w.logs.found {
		u.Warn("found no log stream %s in group %s; check the container's awslogs group and stream prefix", w.logs.stream, w.logs.group)
	}
}
