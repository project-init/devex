package pgaccess

import (
	"fmt"
	"os"
	"os/exec"
)

func RunPsql(psqlCfg *PsqlConfig) error {
	command := fmt.Sprintf("host=%s dbname=%s user=%s password='%s' port=5432 sslmode=%s", psqlCfg.Host, psqlCfg.Database, psqlCfg.Username, psqlCfg.Password, psqlCfg.SSLMode)
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
