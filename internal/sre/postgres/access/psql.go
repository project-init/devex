package access

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"

	"github.com/project-init/devex/internal/sre/config"
)

// psqlCommand builds a psql session for cfg. Connection settings, the password included, go
// through libpq's PG* environment variables rather than argv, where ps would show them.
func psqlCommand(cfg *config.PsqlConfig) *exec.Cmd {
	cmd := exec.Command("psql")
	cmd.Env = append(os.Environ(),
		"PGHOST="+cfg.Host,
		"PGPORT="+strconv.Itoa(cfg.Port),
		"PGUSER="+cfg.Username,
		"PGDATABASE="+cfg.Database,
		"PGPASSWORD="+cfg.Password,
	)
	// Without a configured sslMode, keep the caller's PGSSLMODE, such as a stricter verify-full,
	// or libpq's own default.
	if cfg.SSLMode != "" {
		cmd.Env = append(cmd.Env, "PGSSLMODE="+cfg.SSLMode)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd
}

func runPsql(cfg *config.PsqlConfig) error {
	cmd := psqlCommand(cfg)

	// psql handles Ctrl-C itself by cancelling the running query. Holding a subscription for
	// the session keeps a repeated Ctrl-C from killing devex underneath psql; Ignore would
	// instead pass SIG_IGN on to psql and outlive the session.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	if err := cmd.Run(); err != nil {
		// An *exec.ExitError reads "exit status N" and stays reachable through errors.As.
		return fmt.Errorf("psql failed: %w", err)
	}
	return nil
}
