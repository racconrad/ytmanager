package ytdlp

import (
	"context"
	"os/exec"
)

func newDownloadCommand(ctx context.Context, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, "yt-dlp", args...)
}

func stageWritersStopped(_ *exec.Cmd, cancelled bool) bool {
	// CommandContext terminates only yt-dlp on Windows; a cancelled stage may still have writers.
	return !cancelled
}
