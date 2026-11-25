package ytdlp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ytmanager/internal/device"
)

var audioArgs = map[string][]string{
	"opus": {"--audio-format", "opus"},
	"m4a":  {"--audio-format", "m4a"},
	"mp3":  {"--audio-format", "mp3", "--audio-quality", "0"},
}

func ValidFormat(f string) bool { _, ok := audioArgs[f]; return ok }

// Download fetches the audio described by an enriched info-json into the folders
// layout and returns its final path under root.
//
// yt-dlp runs in a local staging folder and the result is copied to root: on MTP (gvfs) yt-dlp
// cannot move files (no utime) and --windows-filenames would rewrite the ':' in gvfs/mtp:host=….
// Retryable work stays staged. A retry can resume the .part file once its previous writers stop.
func Download(ctx context.Context, stageID, infoJSON, root, format, folders, cookies string, progress func(float64)) (string, error) {
	staging, err := openStageRoot(root, true)
	if err != nil {
		return "", err
	}
	defer staging.Close()
	key := stageKey(stageID, format, folders)
	lock, err := waitStageLock(ctx, staging, key)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	key, err = prepareStage(ctx, staging, key)
	if err != nil {
		return "", err
	}
	discard, writersStopped := false, false
	defer func() {
		if writersStopped && staging.Remove(key+runningSuffix) == nil && discard {
			discardStage(staging, key)
		}
	}()
	stage := filepath.Join(staging.Name(), key)
	file, writersStopped, err := run(ctx, stage, infoJSON, format, folders, cookies, progress)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		retryable := device.IsSpaceError(err) || Transient(err)
		discard = Unavailable(err) && !retryable
		return "", err
	}
	rel, err := filepath.Rel(stage, file)
	if err != nil || !filepath.IsLocal(rel) {
		discard = true
		return "", fmt.Errorf("yt-dlp returned a file outside the staging directory")
	}
	dst, err := copyDownload(file, filepath.Join(root, rel))
	discard = err == nil
	return dst, err
}

// run returns the output path and whether the attempt can release its staging marker.
func run(ctx context.Context, stage, infoJSON, format, folders, cookies string, progress func(float64)) (string, bool, error) {
	args := append(base(cookies),
		"--load-info-json", infoJSON,
		"-f", "bestaudio/best", "-x",
		"--embed-metadata", "--embed-thumbnail", "--convert-thumbnails", "jpg",
		// square cover: center-crop the 16:9 thumbnail
		"--ppa", `ThumbnailsConvertor+FFmpeg_o:-c:v mjpeg -vf crop="'if(gt(ih,iw),iw,ih)':'if(gt(iw,ih),ih,iw)'"`,
		"-P", ".",
		"-o", layouts[folders],
		"--print", "after_move:FILE %(filepath)s",
		"--progress", "--newline",
		"--progress-template", "download:PROG %(progress.downloaded_bytes)s %(progress.total_bytes,progress.total_bytes_estimate)s",
	)
	args = append(append(args, naming...), audioArgs[format]...)

	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	cmd := newDownloadCommand(ctx, args)
	cmd.Dir = stage
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return "", true, processError(err, "could not start yt-dlp")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); pw.Close() }()

	var file string
	var diagnostic diagnostic
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		diagnostic.add(line)
		switch {
		case strings.HasPrefix(line, "PROG "):
			if f := strings.Fields(line); len(f) == 3 {
				got, err1 := strconv.ParseFloat(f[1], 64)
				total, err2 := strconv.ParseFloat(f[2], 64)
				if err1 == nil && err2 == nil && total > 0 {
					progress(got / total)
				}
			}
		case strings.HasPrefix(line, "FILE "):
			file = strings.TrimPrefix(line, "FILE ")
		}
	}
	scanErr := sc.Err()
	if scanErr != nil {
		_, _ = io.Copy(io.Discard, pr)
	} // let the child finish even after an oversized line
	err := <-done
	ctxErr := ctx.Err()
	stopped := stageWritersStopped(cmd, ctxErr != nil) && !errors.Is(err, exec.ErrWaitDelay)
	if ctxErr != nil {
		return "", stopped, ctxErr
	}
	if err != nil {
		return "", stopped && cmd.ProcessState != nil && cmd.ProcessState.Exited(), processError(err, diagnostic.detail())
	}
	if scanErr != nil {
		return "", stopped, fmt.Errorf("could not read yt-dlp output: %w", scanErr)
	}
	if file == "" {
		return "", stopped, fmt.Errorf("yt-dlp did not report the final file")
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(stage, file)
	}
	return file, stopped, nil
}
