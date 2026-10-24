package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/dbstartup"
)

// WorkerConfig is loaded exclusively from a private host file, never job input.
type WorkerConfig struct {
	RepositoryDir      string        `json:"repository_dir"`
	WorktreeRoot       string        `json:"worktree_root"`
	StateDir           string        `json:"state_dir"`
	ProductionEnvFile  string        `json:"production_env_file"`
	ProductionDatabase string        `json:"production_database"`
	CustomRemote       string        `json:"custom_remote"`
	CustomBranch       string        `json:"custom_branch"`
	ServerURL          string        `json:"server_url"`
	WorkerToken        string        `json:"worker_token"`
	PollInterval       time.Duration `json:"poll_interval"`
	DryRun             bool          `json:"-"`
}

var safeRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
var jobID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var databaseName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
var environmentKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func LoadWorkerConfig(path string) (WorkerConfig, error) {
	var cfg WorkerConfig
	if err := privateFile(path); err != nil {
		return cfg, fmt.Errorf("protected config: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()
	dec := json.NewDecoder(io.LimitReader(file, 64<<10))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&cfg); err != nil {
		return WorkerConfig{}, fmt.Errorf("invalid config JSON")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return WorkerConfig{}, fmt.Errorf("unexpected trailing config")
	}
	if err = cfg.Validate(); err != nil {
		return WorkerConfig{}, err
	}
	return cfg, nil
}

func (c WorkerConfig) Validate() error {
	for _, path := range []string{c.RepositoryDir, c.WorktreeRoot, c.StateDir, c.ProductionEnvFile} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
			return fmt.Errorf("host paths must be absolute dedicated paths")
		}
		if err := noSymlinks(path); err != nil {
			return err
		}
	}
	roots := []string{c.RepositoryDir, c.WorktreeRoot, c.StateDir}
	for i, p := range roots {
		for j, q := range roots {
			if i != j && within(p, q) {
				return fmt.Errorf("host state, bare repository and worktree paths must be disjoint")
			}
		}
		if within(c.ProductionEnvFile, p) {
			return fmt.Errorf("production env must be outside managed source and state")
		}
	}
	if err := privateFile(c.ProductionEnvFile); err != nil {
		return fmt.Errorf("protected production env: %w", err)
	}
	for dir := filepath.Dir(c.ProductionEnvFile); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return fmt.Errorf("production env must be outside source checkouts")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if !databaseName.MatchString(c.ProductionDatabase) {
		return fmt.Errorf("expected production database is required")
	}
	if !validRef(c.CustomBranch) || strings.HasPrefix(c.CustomBranch, "self-update/") {
		return fmt.Errorf("invalid protected custom branch")
	}
	u, err := url.Parse(c.CustomRemote)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
		return fmt.Errorf("custom remote must be a credential-free HTTPS repository URL")
	}
	u, err = url.Parse(c.ServerURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1"))) {
		return fmt.Errorf("server URL must use HTTPS or loopback HTTP")
	}
	if strings.TrimSpace(c.WorkerToken) == "" || strings.ContainsAny(c.WorkerToken, "\r\n") {
		return fmt.Errorf("worker token is required")
	}
	if c.PollInterval < 0 {
		return fmt.Errorf("invalid polling interval")
	}
	return nil
}

func validRef(s string) bool {
	return safeRef.MatchString(s) && !strings.Contains(s, "..") && !strings.Contains(s, "//") && !strings.Contains(s, "@{") && !strings.HasSuffix(s, "/") && !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, ".lock")
}
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func noSymlinks(path string) error {
	for p := path; p != filepath.Dir(p); p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("host paths cannot contain symlinks")
		}
	}
	return nil
}
func privateFile(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("absolute file path required")
	}
	if err := noSymlinks(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("file must be regular and private (0600 or stricter)")
	}
	return nil
}
func privateDir(path string) error {
	if err := noSymlinks(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("managed directory must be private (0700)")
	}
	return nil
}

func candidateEnvironment(path, productionDB string) error {
	if _, err := os.Lstat(filepath.Join(path, ".env")); !os.IsNotExist(err) {
		return fmt.Errorf("candidate .env is forbidden")
	}
	file := filepath.Join(path, ".env.worktree")
	if err := noSymlinks(file); err != nil {
		return err
	}
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("candidate env must be regular")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !environmentKey.MatchString(k) || strings.ContainsAny(v, "`$\r;&|<>(){}") {
			return fmt.Errorf("candidate env must contain literal values")
		}
		switch k {
		case "ENV_FILE", "WORKTREE_ENV_FILE", "MULTICA_SELF_UPDATE_NO_CONTAINER_START", "BASH_ENV", "ENV", "PATH", "HOME", "MAKEFLAGS", "SHELL":
			return fmt.Errorf("candidate env cannot override host execution controls")
		}
		values[k] = strings.Trim(v, "\"'")
	}
	db := values["POSTGRES_DB"]
	u, err := url.Parse(values["DATABASE_URL"])
	if err != nil || !databaseName.MatchString(db) || db == productionDB || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" || strings.TrimPrefix(u.Path, "/") != db {
		return fmt.Errorf("candidate database must be isolated from production")
	}
	// Match migration connection semantics: pgx permits query aliases (including
	// percent-encoded names/values) to override the database in the URI path.
	poolConfig, err := dbstartup.ParsePoolConfig(values["DATABASE_URL"], 0)
	if err != nil || poolConfig.ConnConfig.Database != db || poolConfig.ConnConfig.Database == productionDB {
		return fmt.Errorf("candidate effective database must be isolated from production")
	}
	return nil
}
