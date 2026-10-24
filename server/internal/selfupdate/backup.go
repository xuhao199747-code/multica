package selfupdate

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/dbstartup"
)

// productionValues reads literal Compose inputs, never sourcing host shell code.
func productionValues(cfg WorkerConfig) (map[string]string, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(cfg.ProductionEnvFile)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !environmentKey.MatchString(k) {
			return nil, fmt.Errorf("invalid production env assignment")
		}
		if _, exists := values[k]; exists {
			return nil, fmt.Errorf("duplicate production env assignment")
		}
		values[k] = strings.Trim(v, "\"'")
	}
	if values["POSTGRES_DB"] != cfg.ProductionDatabase {
		return nil, fmt.Errorf("production database does not match protected identity")
	}
	if !databaseName.MatchString(values["POSTGRES_USER"]) {
		return nil, fmt.Errorf("explicit production PostgreSQL user required")
	}
	return values, nil
}
func composeArgs(cfg WorkerConfig, args ...string) []string {
	return append([]string{"compose", "--env-file", cfg.ProductionEnvFile, "-p", "multica", "-f", "docker-compose.selfhost.yml", "-f", "docker-compose.selfhost.build.yml"}, args...)
}
func checkedRun(ctx context.Context, r CommandRunner, name string, args []string, dir string, env []string) (CommandResult, error) {
	result, err := r.Run(ctx, name, args, dir, env)
	if err != nil || result.ExitCode != 0 {
		return result, fmt.Errorf("%s command failed", name)
	}
	return result, nil
}
func runningDatabase(ctx context.Context, r CommandRunner, cfg WorkerConfig, dir string, env []string) error {
	result, err := checkedRun(ctx, r, "docker", composeArgs(cfg, "exec", "-T", "backend", "printenv", "DATABASE_URL"), dir, env)
	if err != nil {
		return err
	}
	value := strings.TrimSpace(result.Stdout)
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return fmt.Errorf("invalid running database identity")
	}
	parsed, err := dbstartup.ParsePoolConfig(value, 0)
	if err != nil || parsed.ConnConfig.Database != cfg.ProductionDatabase {
		return fmt.Errorf("running database differs from protected identity")
	}
	return nil
}

// BackupDatabase never starts or replaces a container. RunToFile must exclusively
// create the output and finish successfully before this function returns evidence.
func BackupDatabase(ctx context.Context, r CommandRunner, cfg WorkerConfig, dir string) (string, error) {
	values, err := productionValues(cfg)
	if err != nil {
		return "", err
	}
	env := commandEnvironment()
	if err = runningDatabase(ctx, r, cfg, dir, env); err != nil {
		return "", err
	}
	root := filepath.Join(cfg.StateDir, "backups")
	if err = privateDir(root); err != nil {
		return "", err
	}
	// Reserve a unique name, then use a fresh child path for exclusive RunToFile.
	backupDir, err := os.MkdirTemp(root, "database-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(backupDir, "database.dump")
	args := composeArgs(cfg, "exec", "-T", "postgres", "pg_dump", "--format=custom", "--username", values["POSTGRES_USER"], "--dbname", cfg.ProductionDatabase)
	if err = r.RunToFile(ctx, "docker", args, dir, env, path); err != nil {
		return "", fmt.Errorf("database backup failed")
	}
	if err = privateFile(path); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return "", fmt.Errorf("database backup is empty")
	}
	return path, nil
}
