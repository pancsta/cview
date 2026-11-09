//go:build windows

package tcelllivepage

import (
	"context"
	"fmt"
	"os/exec"
)

// RunCmd is not supported on Windows, because the livepage bridge uses a Unix
// PTY.
func RunCmd(_ context.Context, _ *exec.Cmd, _ CmdOpts) error {
	return fmt.Errorf("livepage is not supported on Windows")
}
