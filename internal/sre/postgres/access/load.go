package access

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/gommon/pkg/aws"
	"github.com/project-init/gommon/pkg/postgres"
)

func loadPGAccessEnvironment(environmentConfig config.PostgresEnvironmentConfig) (*config.PsqlConfig, error) {
	fmt.Printf("Logging in to %s as %s via psql.\n", environmentConfig.Host, environmentConfig.UserName)

	password, err := getPassword(environmentConfig)
	if err != nil {
		return nil, err
	}

	return &config.PsqlConfig{
		Host:     environmentConfig.Host,
		Port:     environmentConfig.Port,
		Username: environmentConfig.UserName,
		Password: password,
		Database: environmentConfig.Database,
		SSLMode:  environmentConfig.SSLMode,
	}, nil
}

func resolveEnvPassword(password string) (string, error) {
	if strings.HasPrefix(password, "$$") && strings.HasSuffix(password, "$$") && len(password) > 4 {
		varName := password[2 : len(password)-2]
		value, ok := os.LookupEnv(varName)
		if !ok {
			return "", fmt.Errorf("environment variable %q not set", varName)
		}
		return value, nil
	}
	return password, nil
}

func getPassword(environmentConfig config.PostgresEnvironmentConfig) (string, error) {
	if environmentConfig.Password != nil {
		return resolveEnvPassword(*environmentConfig.Password)
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
