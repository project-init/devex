package access

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/gommon/pkg/postgres"
)

const (
	defaultPort = 5432
	// defaultRegion keeps configs written before region existed signing for the region devex
	// always used.
	defaultRegion = "us-east-1"
)

// awsLoader loads the standard AWS configuration; tests swap it out.
type awsLoader func(ctx context.Context) (aws.Config, error)

func loadAWSConfig(ctx context.Context) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx)
}

func loadPGAccessEnvironment(ctx context.Context, env config.PostgresEnvironmentConfig, loadAWS awsLoader) (*config.PsqlConfig, error) {
	// Resolve the port once, so psql and the IAM token always agree on it.
	env.Port = cmp.Or(env.Port, defaultPort)
	password, err := getPassword(ctx, env, loadAWS)
	if err != nil {
		return nil, err
	}
	return &config.PsqlConfig{
		Host:     env.Host,
		Port:     env.Port,
		Username: env.UserName,
		Password: password,
		Database: env.Database,
		SSLMode:  env.SSLMode,
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

// getPassword returns the configured password, or an RDS IAM auth token signed for the
// environment's port and region.
func getPassword(ctx context.Context, env config.PostgresEnvironmentConfig, loadAWS awsLoader) (string, error) {
	if env.Password != nil {
		return resolveEnvPassword(*env.Password)
	}
	if env.Iam == nil || !*env.Iam {
		return "", fmt.Errorf("needs a password or iam: true")
	}

	awsCfg, err := loadAWS(ctx)
	if err != nil {
		return "", fmt.Errorf("load AWS configuration: %w", err)
	}
	region := cmp.Or(env.Region, defaultRegion)
	token, err := postgres.BuildAuthToken(ctx, postgres.ConnectionConfig{
		Host:     env.Host,
		Port:     strconv.Itoa(env.Port),
		User:     env.UserName,
		Database: env.Database,
	}, postgres.IAMConfig{Credentials: awsCfg.Credentials, Region: region})
	if err != nil {
		return "", fmt.Errorf("build IAM auth token: %w", err)
	}
	return token, nil
}
