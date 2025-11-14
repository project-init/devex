package aiprompt

type Prompt struct {
	Args     []Arg  `yaml:"args"`
	Template string `yaml:"template"`
}

type Arg struct {
	Query   string   `yaml:"query"`
	Options []string `yaml:"options"`
}
