//go:build linux || darwin

package ytdlp

import (
	"context"
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func newDownloadCommand(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			return err
		}
		if err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd
}

func stageWritersStopped(cmd *exec.Cmd, _ bool) bool {
	return errors.Is(unix.Kill(-cmd.Process.Pid, 0), unix.ESRCH)
}
