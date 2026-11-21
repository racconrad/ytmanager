package tui

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

type coverOpenedMsg struct {
	collection collectionState
	err        error
}

func openCover(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("the source did not provide a valid HTTPS image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "open", u.String()).Run()
	case "windows":
		return exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", u.String()).Run()
	default:
		return exec.CommandContext(ctx, "xdg-open", u.String()).Run()
	}
}
