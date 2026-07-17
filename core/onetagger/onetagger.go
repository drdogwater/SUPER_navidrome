// Package onetagger shells out to the onetagger-cli binary (https://github.com/Marekkon5/onetagger)
// to automatically tag audio files. It mirrors the external-binary integration pattern used by
// core/ffmpeg.
package onetagger

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/log"
)

type OneTagger interface {
	// Tag runs the OneTagger autotagger against every audio file in dir, matching
	// against the given comma-separated list of platforms (e.g. "musicbrainz").
	Tag(ctx context.Context, dir string, platforms []string) error
	CmdPath() (string, error)
	IsAvailable() bool
}

func New() OneTagger {
	return &oneTagger{}
}

type oneTagger struct{}

func (e *oneTagger) CmdPath() (string, error) {
	return oneTaggerCmd()
}

func (e *oneTagger) IsAvailable() bool {
	_, err := oneTaggerCmd()
	return err == nil
}

func (e *oneTagger) Tag(ctx context.Context, dir string, platforms []string) error {
	cmd, err := oneTaggerCmd()
	if err != nil {
		return fmt.Errorf("onetagger-cli not available: %w", err)
	}
	if len(platforms) == 0 {
		platforms = []string{"musicbrainz"}
	}

	args := []string{
		"autotagger",
		"--path", dir,
		"--platforms", strings.Join(platforms, ","),
		"--overwrite",
		"--album-art-file",
		// OneTagger's own default --tags list is tuned for DJ/electronic-music tagging
		// (genre, bpm, style, label, releaseDate) and omits title/artist/album entirely,
		// so without this, only the year ends up written. Request every field the review
		// screen (and readTags below) actually uses. albumArt must be listed explicitly too:
		// it's what gates OneTagger's entire cover-art step (both the embedded ID3 tag and
		// the --album-art-file cover.jpg) — without it here, no art is ever written even
		// though MusicBrainz found a match.
		"--tags", "title,artist,album,albumArtist,genre,releaseDate,trackNumber,albumArt",
		// Downloaded files have no pre-existing tags. Real YouTube video titles routinely
		// have more than one " - " separator (e.g. "Artist - Title - from Album Name..."),
		// which breaks regex-based filename parsing (it grabs the wrong split, feeds a
		// garbled search query to MusicBrainz, and the file gets silently skipped with no
		// tags at all). Audio fingerprinting via Shazam is immune to messy titles, so it's
		// forced on for every file rather than used as a parse-failure fallback.
		"--enable-shazam",
		"--force-shazam",
	}
	log.Debug(ctx, "Executing onetagger-cli command", "args", args)
	c := exec.CommandContext(ctx, cmd, args...) // #nosec
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("onetagger-cli failed: %w: %s", err, tail(string(out), 4096))
	}
	return nil
}

func tail(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

func oneTaggerCmd() (string, error) {
	otOnce.Do(func() {
		if conf.Server.OneTaggerPath != "" {
			oneTaggerPath = conf.Server.OneTaggerPath
			oneTaggerPath, oneTaggerErr = exec.LookPath(oneTaggerPath)
		} else {
			oneTaggerPath, oneTaggerErr = exec.LookPath("onetagger-cli")
			if errors.Is(oneTaggerErr, exec.ErrDot) {
				oneTaggerPath, oneTaggerErr = exec.LookPath("./onetagger-cli")
			}
		}
		if oneTaggerErr == nil {
			log.Info("Found onetagger-cli", "path", oneTaggerPath)
		}
	})
	return oneTaggerPath, oneTaggerErr
}

// These variables are accessible here for tests. Do not use them directly in production code.
// Use oneTaggerCmd() instead.
var (
	otOnce        sync.Once
	oneTaggerPath string
	oneTaggerErr  error
)
