//go:build darwin

package localsync

import (
	"fmt"
	"os/exec"
)

const nativeTrashScript = `
ObjC.import("Foundation");
function run(argv) {
	const path = $(argv[0]);
	const url = $.NSURL.fileURLWithPath(path);
	const result = Ref();
	const error = Ref();
	const ok = $.NSFileManager.defaultManager.trashItemAtURLResultingItemURLError(url, result, error);
	if (!ok) {
		throw new Error(ObjC.unwrap(error[0].localizedDescription));
	}
}
`

func defaultMoveToTrash(path string) error {
	if output, err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", nativeTrashScript, path).CombinedOutput(); err != nil {
		return fmt.Errorf("move %q to Trash: %w: %s", path, err, output)
	}
	return nil
}
