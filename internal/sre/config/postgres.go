package config

type PostgresConfiguration struct {
	Environments map[string]PostgresEnvironmentConfig `yaml:"environments"`
}

type PostgresEnvironmentConfig struct {
	Host     string  `yaml:"host"`
	Database string  `yaml:"database"`
	Port     int     `yaml:"port"`
	SSLMode  string  `yaml:"sslMode"`
	UserName string  `yaml:"username"`
	Password *string `yaml:"password"`
	Iam      *bool   `yaml:"iam"`
	// Region signs IAM auth tokens. It defaults to us-east-1.
	Region string `yaml:"region"`
}

type PsqlConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	Database string
	SSLMode  string
}
