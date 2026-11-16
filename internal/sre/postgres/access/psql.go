package access

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"github.com/project-init/devex/internal/sre/config"
)

func runPsql(psqlCfg *config.PsqlConfig) error {
	command := fmt.Sprintf("host=%s dbname=%s user=%s password='%s' port=%d sslmode=%s", psqlCfg.Host, psqlCfg.Database, psqlCfg.Username, psqlCfg.Password, psqlCfg.Port, psqlCfg.SSLMode)
	cmd := exec.Command("psql", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// psql handles Ctrl-C itself by cancelling the running query. Holding a subscription for
	// the session keeps a repeated Ctrl-C from killing devex underneath psql; Ignore would
	// instead pass SIG_IGN on to psql and outlive the session.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

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
