package config

type PostgresConfiguration struct {
	Environments map[string]PostgresEnvironmentConfig `yaml:"environments"`
}

type PostgresEnvironmentConfig struct {
	Host     string  `yaml:"host"`
	Database string  `yaml:"database"`
	SSLMode  string  `yaml:"sslMode"`
	UserName string  `yaml:"username"`
	Password *string `yaml:"password"`
	Iam      *bool   `yaml:"iam" default:"false"`
}

type PsqlConfig struct {
	Host     string
	Username string
	Password string
	Database string
	SSLMode  string
}
