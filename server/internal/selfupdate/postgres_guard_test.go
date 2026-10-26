package selfupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCandidatePostgresNeverStartsContainers(t *testing.T) {
	for _, state := range []string{"running", "missing", "unready"} {
		t.Run(state, func(t *testing.T) {
			root := canonicalTempDir(t)
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			stub := `#!/bin/bash
printf '%s\n' "$*" >> "$DOCKER_LOG"
case "$*" in
 *"ps -q --status running postgres"*) if [ "$DOCKER_STATE" != missing ]; then printf 'container-id\n'; fi ;;
 *pg_isready*) if [ "$DOCKER_STATE" = unready ]; then exit 1; fi ;;
 *"SELECT 1 FROM pg_database"*) printf '1\n' ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			envFile := filepath.Join(root, ".env.worktree")
			if err := os.WriteFile(envFile, []byte("POSTGRES_DB=candidate_database\nPOSTGRES_USER=multica\nDATABASE_URL=postgres://multica@localhost:5432/candidate_database\n"), 0600); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "docker.log")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", filepath.Join("..", "..", "..", "scripts", "ensure-postgres.sh"), envFile)
			cmd.Env = append(commandEnvironment(), "PATH="+bin+":"+os.Getenv("PATH"), "DOCKER_LOG="+log, "DOCKER_STATE="+state, "MULTICA_SELF_UPDATE_NO_CONTAINER_START=1")
			output, err := cmd.CombinedOutput()
			if (err != nil) != (state != "running") {
				t.Fatalf("state=%s error=%v output=%s", state, err, output)
			}
			calls, _ := os.ReadFile(log)
			for _, forbidden := range []string{"compose up", "compose start", "CREATE DATABASE \"production\""} {
				if strings.Contains(string(calls), forbidden) {
					t.Fatalf("unsafe postgres command: %s", calls)
				}
			}
			if ctx.Err() != nil {
				t.Fatal("unbounded database readiness wait")
			}
		})
	}
}
