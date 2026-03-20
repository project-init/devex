package analyze

type ProtoBufBuildTool string

const (
	Buf ProtoBufBuildTool = "buf"
)

var AllowableProtoBufBuildTools = []ProtoBufBuildTool{Buf}

type ProtosConfiguration struct {
	Enabled   *bool             `yaml:"enabled"`
	BuildTool ProtoBufBuildTool `yaml:"buildTool"`
}
