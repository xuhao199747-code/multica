//go:build darwin

package localsync

import (
	"fmt"
	"os/exec"
)

func defaultMoveToTrash(path string) error {
	script := `on run argv
	tell application "Finder" to delete POSIX file (item 1 of argv)
end run`
	if output, err := exec.Command("/usr/bin/osascript", "-e", script, path).CombinedOutput(); err != nil {
		return fmt.Errorf("move %q to Trash: %w: %s", path, err, output)
	}
	return nil
}
