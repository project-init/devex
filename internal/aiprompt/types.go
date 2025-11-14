package aiprompt

type Prompt struct {
	Agent    AiAgent `yaml:"agent"`
	Args     []Arg   `yaml:"args"`
	Template string  `yaml:"template"`
}

type AiAgent struct {
	AgentName string   `yaml:"name" default:"gemini"`
	Arguments []string `yaml:"arguments" default:"[-i]"`
}

type Arg struct {
	Query   string   `yaml:"query"`
	Options []string `yaml:"options"`
}
