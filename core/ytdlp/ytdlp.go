// Package ytdlp shells out to the yt-dlp binary to download audio from YouTube.
// It mirrors the external-binary integration pattern used by core/ffmpeg.
package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/log"
)

// ErrUnsupportedHost is returned when the given URL is not a recognized YouTube URL.
var ErrUnsupportedHost = errors.New("URL must be a youtube.com, youtu.be or music.youtube.com link")

// allowedHosts restricts this feature to YouTube, even though yt-dlp itself supports
// hundreds of sites. Without this check, exposing yt-dlp behind an authenticated
// endpoint would effectively turn it into a generic download-anything tool.
var allowedHosts = map[string]bool{
	"youtube.com":       true,
	"www.youtube.com":   true,
	"m.youtube.com":     true,
	"music.youtube.com": true,
	"youtu.be":          true,
}

type YtDlp interface {
	// Download downloads the best audio track(s) from a YouTube URL into outputDir,
	// converting each to mp3. If the URL refers to a playlist (or a video that's part
	// of one), every item in the playlist is downloaded. Returns the paths of the
	// resulting files, in download order.
	Download(ctx context.Context, videoURL, outputDir string) ([]string, error)
	CmdPath() (string, error)
	IsAvailable() bool
}

func New() YtDlp {
	return &ytdlp{}
}

type ytdlp struct{}

// ValidateURL ensures the given string is an http(s) URL pointing at a YouTube host.
func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrUnsupportedHost
	}
	if !allowedHosts[strings.ToLower(u.Hostname())] {
		return nil, ErrUnsupportedHost
	}
	return u, nil
}

func (e *ytdlp) CmdPath() (string, error) {
	return ytdlpCmd()
}

func (e *ytdlp) IsAvailable() bool {
	_, err := ytdlpCmd()
	return err == nil
}

func (e *ytdlp) Download(ctx context.Context, videoURL, outputDir string) ([]string, error) {
	cmd, err := ytdlpCmd()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp not available: %w", err)
	}
	u, err := ValidateURL(videoURL)
	if err != nil {
		return nil, err
	}

	outputTemplate := filepath.Join(outputDir, "%(title)s [%(id)s].%(ext)s")
	args := []string{
		"-x",
		"--audio-format", "mp3",
		"--audio-quality", "0",
		// No --no-playlist: a playlist URL (or a video URL that's part of one) downloads
		// every item, so users can pull a full album/playlist in one go.
		"--quiet",
		"--no-warnings",
		"-o", outputTemplate,
		"--print", "after_move:filepath",
		u.String(),
	}
	log.Debug(ctx, "Executing yt-dlp command", "args", args)
	c := exec.CommandContext(ctx, cmd, args...) // #nosec
	var stdout bytes.Buffer
	stderr := &limitedWriter{limit: 4096}
	c.Stdout = &stdout
	c.Stderr = stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	paths := nonEmptyLines(stdout.String())
	if len(paths) == 0 {
		return nil, errors.New("yt-dlp did not report any downloaded files")
	}
	return paths, nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// limitedWriter caps how much of yt-dlp's stderr is retained for error messages.
type limitedWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if remaining := w.limit - w.buf.Len(); remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		w.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (w *limitedWriter) String() string {
	return w.buf.String()
}

func ytdlpCmd() (string, error) {
	ytOnce.Do(func() {
		if conf.Server.YtDlpPath != "" {
			ytdlpPath = conf.Server.YtDlpPath
			ytdlpPath, ytdlpErr = exec.LookPath(ytdlpPath)
		} else {
			ytdlpPath, ytdlpErr = exec.LookPath("yt-dlp")
			if errors.Is(ytdlpErr, exec.ErrDot) {
				ytdlpPath, ytdlpErr = exec.LookPath("./yt-dlp")
			}
		}
		if ytdlpErr == nil {
			log.Info("Found yt-dlp", "path", ytdlpPath)
		}
	})
	return ytdlpPath, ytdlpErr
}

// These variables are accessible here for tests. Do not use them directly in production code. Use ytdlpCmd() instead.
var (
	ytOnce    sync.Once
	ytdlpPath string
	ytdlpErr  error
)
