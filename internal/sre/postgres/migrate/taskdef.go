package migrate

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// registerInputFrom copies every field a registration shares with td, so the new revision matches
// its source in everything but the image the caller swaps in. It leaves tags off: tagging on
// registration needs ecs:TagResource, which a role that only registers may lack.
func registerInputFrom(td *ecstypes.TaskDefinition) *ecs.RegisterTaskDefinitionInput {
	return &ecs.RegisterTaskDefinitionInput{
		ContainerDefinitions:    td.ContainerDefinitions,
		Family:                  td.Family,
		Cpu:                     td.Cpu,
		EnableFaultInjection:    td.EnableFaultInjection,
		EphemeralStorage:        td.EphemeralStorage,
		ExecutionRoleArn:        td.ExecutionRoleArn,
		InferenceAccelerators:   td.InferenceAccelerators, //nolint:staticcheck // deprecated, but a definition that still has accelerators keeps them
		IpcMode:                 td.IpcMode,
		Memory:                  td.Memory,
		NetworkMode:             td.NetworkMode,
		PidMode:                 td.PidMode,
		PlacementConstraints:    td.PlacementConstraints,
		ProxyConfiguration:      td.ProxyConfiguration,
		RequiresCompatibilities: td.RequiresCompatibilities,
		RuntimePlatform:         td.RuntimePlatform,
		TaskRoleArn:             td.TaskRoleArn,
		Volumes:                 td.Volumes,
	}
}

// findDefinition returns the named container's definition within containers, or nil.
func findDefinition(containers []ecstypes.ContainerDefinition, name string) *ecstypes.ContainerDefinition {
	for i := range containers {
		if aws.ToString(containers[i].Name) == name {
			return &containers[i]
		}
	}
	return nil
}

// logTarget is where the awslogs driver writes one container's output.
type logTarget struct {
	group        string
	streamPrefix string
	region       string
}

// logTargetFor reads the container's awslogs options. It returns nil when the container logs
// elsewhere, or awslogs has no stream prefix, which leaves stream names unpredictable.
func logTargetFor(container *ecstypes.ContainerDefinition) *logTarget {
	config := container.LogConfiguration
	if config == nil || config.LogDriver != ecstypes.LogDriverAwslogs {
		return nil
	}
	options := config.Options
	if options["awslogs-group"] == "" || options["awslogs-stream-prefix"] == "" {
		return nil
	}
	return &logTarget{group: options["awslogs-group"], streamPrefix: options["awslogs-stream-prefix"], region: options["awslogs-region"]}
}

// stream is the log stream awslogs writes for container in the task: prefix/container/task-id.
func (t *logTarget) stream(container, taskARN string) string {
	return t.streamPrefix + "/" + container + "/" + taskARN[strings.LastIndex(taskARN, "/")+1:]
}
