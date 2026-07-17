// Package musicbrainz is a minimal client for the public MusicBrainz web service
// (https://musicbrainz.org/doc/MusicBrainz_API) and its companion Cover Art Archive
// (https://coverartarchive.org). It exists to fetch a release's canonical tracklist
// and cover art so a YouTube playlist can be re-tagged against a real album, which
// OneTagger's per-file autotagger (core/onetagger) doesn't provide: it tags each
// downloaded file independently and never returns a release's full tracklist or a
// single shared cover for the whole album.
package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/navidrome/navidrome/consts"
	"github.com/navidrome/navidrome/log"
)

// ErrReleaseNotFound is returned when no release matches the given album/artist.
var ErrReleaseNotFound = errors.New("no matching release found on MusicBrainz")

// baseURL is the public MusicBrainz web service root. There is no self-hosted
// alternative in this codebase, unlike ffmpeg/yt-dlp/onetagger-cli.
const baseURL = "https://musicbrainz.org/ws/2"

// coverArtArchiveURL is MusicBrainz's official companion image service, keyed by
// release MBID rather than a separate search.
const coverArtArchiveURL = "https://coverartarchive.org"

// maxCoverArtBytes caps how much of a Cover Art Archive response is read into memory.
// Well above any reasonable album cover (a 3000x3000 JPEG is typically a few MB).
const maxCoverArtBytes = 15 << 20 // 15MB

// userAgent identifies this client to MusicBrainz, as required by their API usage
// policy (https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) - unidentified
// clients are rate-limited more aggressively.
var userAgent = fmt.Sprintf("%s/1.0 ( https://github.com/navidrome/navidrome )", consts.AppName)

// ReleaseTrack is one track of a Release's canonical tracklist, in disc/position order.
type ReleaseTrack struct {
	Number int
	Title  string
}

// Release is a MusicBrainz release (a specific album pressing), with its full tracklist.
type Release struct {
	MBID   string
	Title  string
	Artist string
	Year   string
	Tracks []ReleaseTrack
}

type Client interface {
	// FindRelease searches for the release best matching album and artist, and
	// returns it with its full tracklist. Returns ErrReleaseNotFound if nothing matches.
	FindRelease(ctx context.Context, album, artist string) (*Release, error)
	// FetchCoverArt fetches a release's front cover from the Cover Art Archive.
	// Returns nil, nil (not an error) if the release has no cover art registered.
	FetchCoverArt(ctx context.Context, mbid string) ([]byte, error)
}

func New() Client {
	return &client{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

type client struct {
	httpClient *http.Client
}

func (c *client) FindRelease(ctx context.Context, album, artist string) (*Release, error) {
	id, err := c.searchRelease(ctx, album, artist)
	if err != nil {
		return nil, err
	}
	return c.lookupRelease(ctx, id)
}

// searchReleaseResponse is the subset of MusicBrainz's release search response used here.
// https://musicbrainz.org/doc/MusicBrainz_API/Search#Release
type searchReleaseResponse struct {
	Releases []struct {
		ID string `json:"id"`
	} `json:"releases"`
}

func (c *client) searchRelease(ctx context.Context, album, artist string) (string, error) {
	query := fmt.Sprintf(`release:%s AND artist:%s`, quoteLuceneTerm(album), quoteLuceneTerm(artist))
	params := url.Values{
		"query": {query},
		"fmt":   {"json"},
		"limit": {"1"},
	}
	var res searchReleaseResponse
	if err := c.get(ctx, baseURL+"/release?"+params.Encode(), &res); err != nil {
		return "", err
	}
	if len(res.Releases) == 0 {
		return "", ErrReleaseNotFound
	}
	return res.Releases[0].ID, nil
}

// lookupReleaseResponse is the subset of MusicBrainz's release lookup response used here.
// https://musicbrainz.org/doc/MusicBrainz_API/Lookups#Release
type lookupReleaseResponse struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Date         string `json:"date"`
	ArtistCredit []struct {
		Name string `json:"name"`
	} `json:"artist-credit"`
	Media []struct {
		Tracks []struct {
			Position int    `json:"position"`
			Title    string `json:"title"`
		} `json:"tracks"`
	} `json:"media"`
}

func (c *client) lookupRelease(ctx context.Context, id string) (*Release, error) {
	params := url.Values{
		"fmt": {"json"},
		"inc": {"recordings artist-credits"},
	}
	var res lookupReleaseResponse
	if err := c.get(ctx, baseURL+"/release/"+url.PathEscape(id)+"?"+params.Encode(), &res); err != nil {
		return nil, err
	}

	release := &Release{
		MBID:  res.ID,
		Title: res.Title,
		Year:  year(res.Date),
	}
	if len(res.ArtistCredit) > 0 {
		release.Artist = res.ArtistCredit[0].Name
	}
	// A multi-disc release numbers each disc's tracks starting from 1; renumbering
	// sequentially across every disc keeps position consistent with the flat,
	// single-directory download produced by ytdlp.Download.
	trackNumber := 0
	for _, medium := range res.Media {
		for _, t := range medium.Tracks {
			trackNumber++
			release.Tracks = append(release.Tracks, ReleaseTrack{Number: trackNumber, Title: t.Title})
		}
	}
	return release, nil
}

func (c *client) FetchCoverArt(ctx context.Context, mbid string) ([]byte, error) {
	rawURL := coverArtArchiveURL + "/release/" + url.PathEscape(mbid) + "/front"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building Cover Art Archive request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	log.Debug(ctx, "Fetching cover art from Cover Art Archive", "url", rawURL)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying Cover Art Archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Cover Art Archive request failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverArtBytes))
	if err != nil {
		return nil, fmt.Errorf("reading cover art: %w", err)
	}
	return data, nil
}

func (c *client) get(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("building MusicBrainz request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	log.Debug(ctx, "Querying MusicBrainz", "url", rawURL)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("querying MusicBrainz: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrReleaseNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MusicBrainz request failed: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding MusicBrainz response: %w", err)
	}
	return nil
}

// quoteLuceneTerm wraps a search term in quotes for MusicBrainz's Lucene-based query
// syntax, escaping any quotes it already contains, so album/artist names containing
// spaces or special characters are matched as a literal phrase rather than parsed as
// query syntax.
func quoteLuceneTerm(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// year extracts the 4-digit year from a MusicBrainz date, which may be a bare year,
// "YYYY-MM", or "YYYY-MM-DD".
func year(date string) string {
	if len(date) < 4 {
		return ""
	}
	if _, err := strconv.Atoi(date[:4]); err != nil {
		return ""
	}
	return date[:4]
}
