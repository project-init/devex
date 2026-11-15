package access

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/project-init/devex/internal/sre/config"
)

func staticAWS(region string) awsLoader {
	return func(context.Context) (aws.Config, error) {
		return aws.Config{
			Region:      region,
			Credentials: credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", ""),
		}, nil
	}
}

func TestIAMTokenUsesConfiguredPortAndRegion(t *testing.T) {
	iam := true
	env := config.PostgresEnvironmentConfig{Host: "db.example.com", Port: 6543, UserName: "app", Database: "orders", Iam: &iam, Region: "eu-west-1"}
	token, err := getPassword(context.Background(), env, staticAWS("us-east-1"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse("https://" + token)
	if err != nil {
		t.Fatalf("token %q is not a presigned URL: %v", token, err)
	}
	if parsed.Host != "db.example.com:6543" {
		t.Errorf("token host = %q, want db.example.com:6543", parsed.Host)
	}
	if credential := parsed.Query().Get("X-Amz-Credential"); !strings.Contains(credential, "/eu-west-1/rds-db/") {
		t.Errorf("token credential = %q, want it signed for eu-west-1", credential)
	}
}

func TestIAMTokenDefaultsRegionAndPort(t *testing.T) {
	iam := true
	env := config.PostgresEnvironmentConfig{Host: "db.example.com", UserName: "app", Iam: &iam}
	psqlCfg, err := loadPGAccessEnvironment(context.Background(), env, staticAWS("ap-south-1"))
	if err != nil {
		t.Fatal(err)
	}
	token := psqlCfg.Password
	if psqlCfg.Port != 5432 {
		t.Errorf("psql port = %d, want 5432", psqlCfg.Port)
	}
	// The AWS profile's region is ap-south-1, but an unset environment region keeps the old default.
	if !strings.HasPrefix(token, "db.example.com:5432?") || !strings.Contains(token, "%2Fus-east-1%2Frds-db%2F") {
		t.Errorf("token = %q, want port 5432 and region us-east-1", token)
	}
}

func TestAWSConfigErrorIsReturned(t *testing.T) {
	iam := true
	failing := func(context.Context) (aws.Config, error) { return aws.Config{}, errors.New("bad profile") }
	_, err := getPassword(context.Background(), config.PostgresEnvironmentConfig{Iam: &iam}, failing)
	if err == nil || !strings.Contains(err.Error(), "bad profile") {
		t.Errorf("getPassword() error = %v, want the AWS configuration error", err)
	}
}

func TestPsqlCommandKeepsPasswordOutOfArgs(t *testing.T) {
	cmd := psqlCommand(&config.PsqlConfig{Host: "db", Port: 5432, Username: "app", Database: "orders", Password: "it's secret", SSLMode: "require"})
	if strings.Contains(strings.Join(cmd.Args, " "), "secret") {
		t.Errorf("psql args %q contain the password", cmd.Args)
	}
	for _, want := range []string{"PGPASSWORD=it's secret", "PGHOST=db", "PGPORT=5432", "PGUSER=app", "PGDATABASE=orders", "PGSSLMODE=require"} {
		if !slices.Contains(cmd.Env, want) {
			t.Errorf("psql env lacks %q", want)
		}
	}
}

func TestRunPsqlReportsExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as psql")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "psql"), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := runPsql(&config.PsqlConfig{Host: "db", Port: 5432})
	var exitErr *exec.ExitError
	if err == nil || !strings.Contains(err.Error(), "status 3") || !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("runPsql() error = %v, want the exit status, wrapped", err)
	}
}

func TestPsqlCommandKeepsCallerSSLModeWhenUnset(t *testing.T) {
	t.Setenv("PGSSLMODE", "verify-full")
	cmd := psqlCommand(&config.PsqlConfig{Host: "localhost"})
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "PGSSLMODE=") && entry != "PGSSLMODE=verify-full" {
			t.Errorf("env has %q, want the caller's verify-full kept", entry)
		}
	}
}
