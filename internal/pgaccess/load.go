package pgaccess

import (
	"context"
	"fmt"
	"os"

	"github.com/project-init/gommon/pkg/aws"
	"github.com/project-init/gommon/pkg/postgres"
	"gopkg.in/yaml.v3"
)

func LoadPGAccessEnvironments(pgAccessFile string) (map[string]EnvironmentConfig, error) {
	bytes, err := os.ReadFile(pgAccessFile)
	if err != nil {
		return nil, err
	}

	var config Configuration
	if err = yaml.Unmarshal(bytes, &config); err != nil {
		return nil, err
	}

	return config.Environments, nil
}

func LoadPGAccessEnvironment(environmentConfig EnvironmentConfig) (*PsqlConfig, error) {
	fmt.Printf("Logging in to %s as %s via psql.\n", environmentConfig.Host, environmentConfig.UserName)

	password, err := getPassword(environmentConfig)
	if err != nil {
		return nil, err
	}

	return &PsqlConfig{
		Host:     environmentConfig.Host,
		Username: environmentConfig.UserName,
		Password: password,
		Database: environmentConfig.Database,
		SSLMode:  environmentConfig.SSLMode,
	}, nil
}

func getPassword(environmentConfig EnvironmentConfig) (string, error) {
	if environmentConfig.Password != nil {
		return *environmentConfig.Password, nil
	}

	if environmentConfig.Iam == nil || !*environmentConfig.Iam {
		return "", fmt.Errorf("require password or use of iam rds connect")
	}

	return postgres.BuildAuthToken(context.Background(), postgres.ConnectionConfig{
		Host:     environmentConfig.Host,
		Port:     "5432",
		User:     environmentConfig.UserName,
		Database: environmentConfig.Database,
		Password: "",
	}, postgres.IAMConfig{
		Credentials: aws.GetConfig().Credentials,
		Region:      "us-east-1",
	})
}
