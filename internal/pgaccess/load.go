package pgaccess

import (
	"context"
	"fmt"
	"os"

	"github.com/project-init/gommon/pkg/aws"
	"github.com/project-init/gommon/pkg/postgres"
	"gopkg.in/yaml.v3"
)

func LoadPGAccessEnvironmentUser(pgAccessFile string, environment string) (*PsqlConfig, error) {
	bytes, err := os.ReadFile(pgAccessFile)
	if err != nil {
		return nil, err
	}

	var config Configuration
	if err = yaml.Unmarshal(bytes, &config); err != nil {
		return nil, err
	}

	environmentConfig, found := config.Environments[environment]
	if !found {
		return nil, fmt.Errorf("environment %s not found in file %s", environment, pgAccessFile)
	}

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
