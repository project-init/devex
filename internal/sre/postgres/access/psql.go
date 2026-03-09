package access

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/project-init/devex/internal/sre/config"
)

func runPsql(psqlCfg *config.PsqlConfig) error {
	command := fmt.Sprintf("host=%s dbname=%s user=%s password='%s' port=%d sslmode=%s", psqlCfg.Host, psqlCfg.Database, psqlCfg.Username, psqlCfg.Password, psqlCfg.Port, psqlCfg.SSLMode)
	cmd := exec.Command("psql", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}

	return nil
}
