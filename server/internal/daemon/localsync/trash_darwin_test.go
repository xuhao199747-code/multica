//go:build darwin

package localsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultMoveToTrashMovesDirectoryWithoutFinder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "deleted-project")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}

	if err := defaultMoveToTrash(dir); err != nil {
		t.Fatalf("move directory to Trash: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory exists after move to Trash: %v", err)
	}
}
