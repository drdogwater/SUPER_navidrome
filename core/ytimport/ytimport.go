// Package ytimport orchestrates the YouTube -> yt-dlp -> OneTagger -> library pipeline:
// download a video or an entire playlist's audio, auto-tag every track, and hold them
// for the user to review before they are moved into the music library and scanned.
package ytimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/core/musicbrainz"
	"github.com/navidrome/navidrome/core/onetagger"
	"github.com/navidrome/navidrome/core/ytdlp"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"go.senan.xyz/taglib"
)

// State is a step in a job's lifecycle: Downloading -> Tagging -> AwaitingReview -> Confirmed/Rejected,
// with Failed reachable from Downloading or Tagging.
type State string

const (
	StateDownloading    State = "downloading"
	StateTagging        State = "tagging"
	StateAwaitingReview State = "awaiting_review"
	StateConfirmed      State = "confirmed"
	StateRejected       State = "rejected"
	StateFailed         State = "failed"
)

// destinationFolder is the subfolder (relative to the default library's root) that
// confirmed downloads are moved into. It's mounted as a separate writable bind mount
// nested inside the (otherwise read-only) music library, so this needs to match the
// subpath used in docker-compose.yml.
const destinationFolder = "youtube-downloads"

var (
	ErrJobNotFound   = errors.New("job not found")
	ErrJobNotReady   = errors.New("job is not awaiting review")
	ErrJobHasNoFile  = errors.New("job has no downloaded files")
	ErrNoCoverArt    = errors.New("track has no embedded cover art")
	ErrAlbumNotFound = errors.New("no matching album found")
)

// Tags is the small, review-friendly subset of metadata exposed to (and editable by) the user.
type Tags struct {
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtist string `json:"albumArtist"`
	Genre       string `json:"genre"`
	Year        string `json:"year"`
	TrackNumber string `json:"trackNumber"`
}

// Track is one downloaded, auto-tagged file awaiting the user's review.
type Track struct {
	ID    string `json:"id"`
	Tags  Tags   `json:"tags"`
	Error string `json:"error,omitempty"`
}

// TrackConfirm carries the user's review decision for one track: whether to include
// it, and any tag edits (blank fields keep the auto-tagged value).
type TrackConfirm struct {
	ID      string `json:"id"`
	Include bool   `json:"include"`
	Tags    Tags   `json:"tags"`
}

// AlbumCandidate is a distinct (album, albumArtist) pair guessed by OneTagger across a
// job's tracks, offered to the user as "this playlist is probably this album". TrackCount
// is how many downloaded tracks OneTagger guessed into this album, out of the job's total
// - a playlist rarely matches 1:1, since a few tracks commonly get a different or no guess.
type AlbumCandidate struct {
	Album       string `json:"album"`
	AlbumArtist string `json:"albumArtist"`
	TrackCount  int    `json:"trackCount"`
}

// AppliedAlbum records which album ApplyAlbum last applied to a job, so the review
// screen can switch from per-track cover editing to a single shared album cover
// (embedded into every track by ApplyAlbum) once one has been confirmed.
type AppliedAlbum struct {
	Album       string `json:"album"`
	AlbumArtist string `json:"albumArtist"`
}

// Job is a point-in-time, JSON-serializable snapshot of a YouTube import, safe to hand
// to callers outside this package.
type Job struct {
	ID              string           `json:"id"`
	URL             string           `json:"url"`
	State           State            `json:"state"`
	Tracks          []Track          `json:"tracks,omitempty"`
	AlbumCandidates []AlbumCandidate `json:"albumCandidates,omitempty"`
	AppliedAlbum    *AppliedAlbum    `json:"appliedAlbum,omitempty"`
	Error           string           `json:"error,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
}

// job is the live, mutable record tracked internally while an import is in flight.
type job struct {
	Job

	dir          string            // staging directory for this job
	files        map[string]string // track ID -> staged file path
	appliedAlbum *AppliedAlbum     // set by ApplyAlbum once a real album has been confirmed
	mu           sync.Mutex
}

type Service interface {
	// Start validates the URL and kicks off the download+tag pipeline in the background,
	// returning immediately with the new job's initial state. A playlist URL (or a video
	// URL that's part of one) downloads every item in the playlist.
	Start(ctx context.Context, videoURL string) (Job, error)
	// Status returns a point-in-time snapshot of a job.
	Status(jobID string) (Job, error)
	// Cover returns the cover art embedded into a staged track (from OneTagger, or a
	// prior SetCover call), so the review screen can preview it before the track is
	// imported. Returns ErrNoCoverArt if MusicBrainz had no art for the track and no
	// custom cover has been set.
	Cover(jobID, trackID string) ([]byte, error)
	// SetCover replaces a staged track's embedded cover art with a user-supplied
	// image, overwriting whatever OneTagger set (or a previous SetCover call). The
	// image is embedded immediately, so it carries through unchanged on Confirm.
	SetCover(jobID, trackID string, image []byte) error
	// SetAlbumCover replaces every one of a job's tracks with the same user-supplied
	// cover image, overwriting whatever ApplyAlbum (or OneTagger) set. Intended for use
	// once an album has been applied and every track is meant to share one cover.
	SetAlbumCover(jobID string, image []byte) error
	// ApplyAlbum looks up album/albumArtist on MusicBrainz and, if found, overwrites
	// every track's tags (title, trackNumber, album, albumArtist, artist, genre, year)
	// by mapping the release's canonical tracklist onto the job's tracks in playlist
	// order. Intended to be called with one of the job's own AlbumCandidates once the
	// user confirms which real album the playlist is. Returns the updated job snapshot.
	// Returns ErrAlbumNotFound if MusicBrainz has no matching release.
	ApplyAlbum(ctx context.Context, jobID, album, albumArtist string) (Job, error)
	// Confirm applies any edited tags to the included tracks, moves them into the
	// library named "Artist - Album - Title", and triggers a scoped scan.
	Confirm(ctx context.Context, jobID string, tracks []TrackConfirm) error
	// Reject discards a job and deletes its staged files.
	Reject(jobID string) error
}

func New(ds model.DataStore, scanner model.Scanner, yt ytdlp.YtDlp, ot onetagger.OneTagger, mb musicbrainz.Client) Service {
	return &service{ds: ds, scanner: scanner, yt: yt, ot: ot, mb: mb, jobs: map[string]*job{}}
}

type service struct {
	ds      model.DataStore
	scanner model.Scanner
	yt      ytdlp.YtDlp
	ot      onetagger.OneTagger
	mb      musicbrainz.Client

	mu   sync.Mutex
	jobs map[string]*job
}

func (s *service) Start(ctx context.Context, videoURL string) (Job, error) {
	if !conf.Server.EnableYoutubeDownload {
		return Job{}, errors.New("the YouTube download feature is disabled")
	}
	if !s.yt.IsAvailable() {
		return Job{}, errors.New("yt-dlp is not installed or not found on PATH")
	}
	if !s.ot.IsAvailable() {
		return Job{}, errors.New("onetagger-cli is not installed or not found on PATH")
	}
	if _, err := ytdlp.ValidateURL(videoURL); err != nil {
		return Job{}, err
	}

	j := &job{Job: Job{
		ID:        uuid.NewString(),
		URL:       videoURL,
		State:     StateDownloading,
		CreatedAt: time.Now(),
	}}
	s.mu.Lock()
	s.jobs[j.ID] = j
	s.mu.Unlock()

	// The pipeline must outlive the HTTP request that started it.
	go s.run(context.WithoutCancel(ctx), j)

	return snapshot(j), nil
}

func (s *service) run(ctx context.Context, j *job) {
	dir, err := s.stagingDir()
	if err != nil {
		s.fail(ctx, j, err)
		return
	}
	jobDir := filepath.Join(dir, j.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		s.fail(ctx, j, fmt.Errorf("creating staging folder: %w", err))
		return
	}
	j.mu.Lock()
	j.dir = jobDir
	j.mu.Unlock()

	paths, err := s.yt.Download(ctx, j.URL, jobDir)
	if err != nil {
		s.fail(ctx, j, err)
		return
	}

	s.setState(j, StateTagging)

	platforms := strings.Split(conf.Server.OneTaggerPlatforms, ",")
	if err := s.ot.Tag(ctx, jobDir, platforms); err != nil {
		s.fail(ctx, j, err)
		return
	}

	tracks := make([]Track, 0, len(paths))
	files := make(map[string]string, len(paths))
	for i, path := range paths {
		id := strconv.Itoa(i)
		track := Track{ID: id}
		if tags, err := readTags(path); err != nil {
			track.Error = err.Error()
		} else {
			track.Tags = tags
		}
		tracks = append(tracks, track)
		files[id] = path
	}

	j.mu.Lock()
	j.files = files
	j.Tracks = tracks
	j.State = StateAwaitingReview
	j.mu.Unlock()
	log.Info(ctx, "YouTube import ready for review", "jobId", j.ID, "url", j.URL, "tracks", len(tracks))
}

func (s *service) Status(jobID string) (Job, error) {
	j, err := s.get(jobID)
	if err != nil {
		return Job{}, err
	}
	return snapshot(j), nil
}

func (s *service) Cover(jobID, trackID string) ([]byte, error) {
	j, err := s.get(jobID)
	if err != nil {
		return nil, err
	}

	j.mu.Lock()
	path, ok := j.files[trackID]
	j.mu.Unlock()
	if !ok {
		return nil, ErrJobHasNoFile
	}

	img, err := taglib.ReadImage(path)
	if err != nil {
		return nil, fmt.Errorf("reading cover art: %w", err)
	}
	if len(img) == 0 {
		return nil, ErrNoCoverArt
	}
	return img, nil
}

func (s *service) SetCover(jobID, trackID string, image []byte) error {
	j, err := s.get(jobID)
	if err != nil {
		return err
	}

	j.mu.Lock()
	path, ok := j.files[trackID]
	j.mu.Unlock()
	if !ok {
		return ErrJobHasNoFile
	}

	if err := taglib.WriteImage(path, image); err != nil {
		return fmt.Errorf("writing cover art: %w", err)
	}
	return nil
}

func (s *service) SetAlbumCover(jobID string, image []byte) error {
	j, err := s.get(jobID)
	if err != nil {
		return err
	}

	j.mu.Lock()
	files := maps.Clone(j.files)
	j.mu.Unlock()
	if len(files) == 0 {
		return ErrJobHasNoFile
	}

	for id, path := range files {
		if err := taglib.WriteImage(path, image); err != nil {
			return fmt.Errorf("writing cover art for track %s: %w", id, err)
		}
	}
	return nil
}

func (s *service) ApplyAlbum(ctx context.Context, jobID, album, albumArtist string) (Job, error) {
	j, err := s.get(jobID)
	if err != nil {
		return Job{}, err
	}

	release, err := s.mb.FindRelease(ctx, album, albumArtist)
	if errors.Is(err, musicbrainz.ErrReleaseNotFound) {
		return Job{}, ErrAlbumNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("looking up album on MusicBrainz: %w", err)
	}

	// Best-effort: a missing or unreachable cover shouldn't block the tag remap below,
	// it just means every track keeps whatever cover it already had (usually OneTagger's).
	var cover []byte
	if release.MBID != "" {
		if cover, err = s.mb.FetchCoverArt(ctx, release.MBID); err != nil {
			log.Warn(ctx, "Could not fetch album cover art from MusicBrainz", "jobId", jobID, "album", release.Title, err)
		}
	}

	j.mu.Lock()
	if j.State != StateAwaitingReview {
		j.mu.Unlock()
		return Job{}, ErrJobNotReady
	}
	applyReleaseToTracks(j.Tracks, release)
	j.appliedAlbum = &AppliedAlbum{Album: release.Title, AlbumArtist: release.Artist}
	files := maps.Clone(j.files)
	j.mu.Unlock()

	// Embeds into every downloaded track, not just the ones applyReleaseToTracks mapped
	// tags onto - it's the same physical release either way, whether or not every track's
	// title/number lined up with the canonical tracklist. Skipped entirely when
	// MusicBrainz has no cover registered, so tracks keep whatever OneTagger already set
	// rather than having it blanked out.
	if len(cover) > 0 {
		for id, path := range files {
			if err := taglib.WriteImage(path, cover); err != nil {
				log.Warn(ctx, "Could not embed album cover art", "jobId", jobID, "trackId", id, err)
			}
		}
	}

	log.Info(ctx, "Applied MusicBrainz album to YouTube import", "jobId", jobID, "album", release.Title, "albumArtist", release.Artist)
	return snapshot(j), nil
}

// applyReleaseToTracks overwrites tracks' tags with release's tracklist, matched by
// playlist position: tracks[i] gets release.Tracks[i]. Extra tracks (in either slice)
// are left as-is - a downloaded playlist rarely lines up 1:1 with the real album (bonus
// tracks, intros, a short release). Genre/year are only overwritten when the release
// actually has one, so a track's existing OneTagger guess isn't blanked out.
func applyReleaseToTracks(tracks []Track, release *musicbrainz.Release) {
	// MusicBrainz commonly credits compilations/various-artist releases as "Various
	// Artists"; overwriting every track's artist with that would destroy the (usually
	// more useful) per-track artist OneTagger already guessed.
	isCompilation := strings.EqualFold(release.Artist, "Various Artists")

	for i := range tracks {
		if i >= len(release.Tracks) {
			break
		}
		rt := release.Tracks[i]
		t := &tracks[i].Tags
		t.Title = rt.Title
		t.TrackNumber = strconv.Itoa(rt.Number)
		t.Album = release.Title
		t.AlbumArtist = release.Artist
		if !isCompilation {
			t.Artist = release.Artist
		}
		if release.Year != "" {
			t.Year = release.Year
		}
	}
}

// albumCandidates groups tracks by their (already auto-tagged) album/albumArtist pair,
// as a shortlist of "this playlist is probably this album" guesses for the user to
// confirm via ApplyAlbum. Tracks with no album guess are excluded. Ordered by how many
// tracks agree, most first, so the likeliest guess is offered first.
func albumCandidates(tracks []Track) []AlbumCandidate {
	type key struct{ album, albumArtist string }
	counts := map[key]int{}
	var order []key
	for _, t := range tracks {
		if t.Tags.Album == "" {
			continue
		}
		k := key{t.Tags.Album, t.Tags.AlbumArtist}
		if _, seen := counts[k]; !seen {
			order = append(order, k)
		}
		counts[k]++
	}
	candidates := make([]AlbumCandidate, 0, len(order))
	for _, k := range order {
		candidates = append(candidates, AlbumCandidate{
			Album:       k.album,
			AlbumArtist: k.albumArtist,
			TrackCount:  counts[k],
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].TrackCount > candidates[j].TrackCount
	})
	return candidates
}

func (s *service) Confirm(ctx context.Context, jobID string, tracks []TrackConfirm) error {
	j, err := s.get(jobID)
	if err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.State != StateAwaitingReview {
		return ErrJobNotReady
	}
	if len(j.files) == 0 {
		return ErrJobHasNoFile
	}

	libPath, err := s.ds.Library(ctx).GetPath(model.DefaultLibraryID)
	if err != nil {
		return fmt.Errorf("resolving library path: %w", err)
	}
	destDir := filepath.Join(libPath, destinationFolder)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating destination folder: %w", err)
	}

	var errs []string
	imported := 0
	for _, tc := range tracks {
		srcPath, ok := j.files[tc.ID]
		if !ok || !tc.Include {
			continue
		}
		if err := importTrack(srcPath, destDir, trackTags(j.Tracks, tc.ID), tc.Tags); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tc.ID, err))
			continue
		}
		imported++
	}

	_ = os.RemoveAll(j.dir)

	if imported == 0 {
		if len(errs) > 0 {
			return fmt.Errorf("no tracks were imported: %s", strings.Join(errs, "; "))
		}
		return errors.New("no tracks selected to import")
	}
	if len(errs) > 0 {
		log.Warn(ctx, "Some YouTube download tracks failed to import", "jobId", j.ID, "errors", errs)
	}

	if _, err := s.scanner.ScanFolders(ctx, false, []model.ScanTarget{
		{LibraryID: model.DefaultLibraryID, FolderPath: destinationFolder},
	}); err != nil {
		log.Error(ctx, "Error scanning newly added YouTube downloads", "jobId", j.ID, err)
	}

	j.State = StateConfirmed
	s.forget(j.ID)
	return nil
}

// importTrack writes any tag edits, then moves the file into destDir named
// "Artist - Album - Title" (falling back gracefully when tags are missing).
func importTrack(srcPath, destDir string, original, edited Tags) error {
	if writeTags := toTagLibMap(edited); len(writeTags) > 0 {
		if err := taglib.WriteTags(srcPath, writeTags, 0); err != nil {
			return fmt.Errorf("writing edited tags: %w", err)
		}
	}
	final := mergeTags(original, edited)
	fileName := buildFileName(final, filepath.Ext(srcPath))
	destPath := uniquePath(filepath.Join(destDir, fileName))
	if err := moveFile(srcPath, destPath); err != nil {
		return fmt.Errorf("moving file into library: %w", err)
	}
	return nil
}

func (s *service) Reject(jobID string) error {
	j, err := s.get(jobID)
	if err != nil {
		return err
	}

	j.mu.Lock()
	if j.State != StateAwaitingReview && j.State != StateFailed {
		j.mu.Unlock()
		return ErrJobNotReady
	}
	dir := j.dir
	j.State = StateRejected
	j.mu.Unlock()

	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	s.forget(jobID)
	return nil
}

func (s *service) stagingDir() (string, error) {
	dir := conf.Server.YoutubeDownloadStagingFolder
	if dir == "" {
		dir = filepath.Join(conf.Server.DataFolder.String(), "youtube-staging")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating staging folder: %w", err)
	}
	return dir, nil
}

func (s *service) get(jobID string) (*job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}
	return j, nil
}

func (s *service) forget(jobID string) {
	s.mu.Lock()
	delete(s.jobs, jobID)
	s.mu.Unlock()
}

func (s *service) setState(j *job, state State) {
	j.mu.Lock()
	j.State = state
	j.mu.Unlock()
}

func (s *service) fail(ctx context.Context, j *job, err error) {
	log.Error(ctx, "YouTube import job failed", "jobId", j.ID, "url", j.URL, err)
	j.mu.Lock()
	j.State = StateFailed
	j.Error = err.Error()
	j.mu.Unlock()
}

// snapshot returns a value copy of a job's exported fields, safe to hand to callers
// without exposing the live, lock-guarded struct.
func snapshot(j *job) Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	jb := Job{
		ID:        j.ID,
		URL:       j.URL,
		State:     j.State,
		Tracks:    append([]Track(nil), j.Tracks...),
		Error:     j.Error,
		CreatedAt: j.CreatedAt,
	}
	if j.State == StateAwaitingReview {
		jb.AlbumCandidates = albumCandidates(j.Tracks)
		jb.AppliedAlbum = j.appliedAlbum
	}
	return jb
}

// trackTags looks up a track's auto-tagged (pre-edit) Tags by ID.
func trackTags(tracks []Track, id string) Tags {
	for _, t := range tracks {
		if t.ID == id {
			return t.Tags
		}
	}
	return Tags{}
}

// mergeTags overlays edited onto original, field by field: a blank edited value keeps
// the original (auto-tagged) value rather than clearing it.
func mergeTags(original, edited Tags) Tags {
	pick := func(o, e string) string {
		if e != "" {
			return e
		}
		return o
	}
	return Tags{
		Title:       pick(original.Title, edited.Title),
		Artist:      pick(original.Artist, edited.Artist),
		Album:       pick(original.Album, edited.Album),
		AlbumArtist: pick(original.AlbumArtist, edited.AlbumArtist),
		Genre:       pick(original.Genre, edited.Genre),
		Year:        pick(original.Year, edited.Year),
		TrackNumber: pick(original.TrackNumber, edited.TrackNumber),
	}
}

// buildFileName renders the final tags as "Artist - Album - Title.ext", dropping any
// segment that's still blank after tagging (e.g. an untagged single with no album).
func buildFileName(t Tags, ext string) string {
	parts := make([]string, 0, 3)
	if artist := sanitizeFileNamePart(t.Artist); artist != "" {
		parts = append(parts, artist)
	}
	if album := sanitizeFileNamePart(t.Album); album != "" {
		parts = append(parts, album)
	}
	title := sanitizeFileNamePart(t.Title)
	if title == "" {
		title = "Unknown Title"
	}
	parts = append(parts, title)
	return strings.Join(parts, " - ") + ext
}

var fileNameReplacer = strings.NewReplacer(
	"/", "-", `\`, "-", ":", "-", "*", "", "?", "",
	`"`, "'", "<", "", ">", "", "|", "-",
)

func sanitizeFileNamePart(s string) string {
	return strings.TrimSpace(fileNameReplacer.Replace(strings.TrimSpace(s)))
}

func readTags(path string) (Tags, error) {
	raw, err := taglib.ReadTags(path)
	if err != nil {
		return Tags{}, fmt.Errorf("reading tags: %w", err)
	}
	get := func(key string) string {
		if v := raw[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	return Tags{
		Title:       get(taglib.Title),
		Artist:      get(taglib.Artist),
		Album:       get(taglib.Album),
		AlbumArtist: get(taglib.AlbumArtist),
		Genre:       get(taglib.Genre),
		Year:        get(taglib.Date),
		TrackNumber: get(taglib.TrackNumber),
	}, nil
}

func toTagLibMap(t Tags) map[string][]string {
	fields := map[string]string{
		taglib.Title:       t.Title,
		taglib.Artist:      t.Artist,
		taglib.Album:       t.Album,
		taglib.AlbumArtist: t.AlbumArtist,
		taglib.Genre:       t.Genre,
		taglib.Date:        t.Year,
		taglib.TrackNumber: t.TrackNumber,
	}
	out := make(map[string][]string, len(fields))
	for k, v := range fields {
		if v != "" {
			out[k] = []string{v}
		}
	}
	return out
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}
