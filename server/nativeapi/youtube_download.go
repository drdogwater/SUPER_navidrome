package nativeapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/navidrome/navidrome/core/ytimport"
	"github.com/navidrome/navidrome/log"
)

// maxCoverUploadSize caps user-supplied cover art. Well above any reasonable album art
// (a 3000x3000 cover.jpg is typically a few MB) while still bounding memory use, since
// the whole upload is read into memory before being embedded into the audio file.
const maxCoverUploadSize = 10 << 20 // 10MB

func (api *Router) addYoutubeDownloadRoute(r chi.Router) {
	r.Route("/youtube-download", func(r chi.Router) {
		r.Post("/", api.startYoutubeDownload())
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", api.getYoutubeDownloadStatus())
			r.Post("/album", api.applyYoutubeDownloadAlbum())
			r.Post("/confirm", api.confirmYoutubeDownload())
			r.Delete("/", api.rejectYoutubeDownload())
			r.Get("/tracks/{trackId}/cover", api.getYoutubeDownloadCover())
			r.Put("/tracks/{trackId}/cover", api.setYoutubeDownloadCover())
			r.Put("/cover", api.setYoutubeDownloadAlbumCover())
		})
	})
}

func (api *Router) startYoutubeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		job, err := api.ytImport.Start(ctx, body.URL)
		if err != nil {
			log.Warn(ctx, "Could not start YouTube download", "url", body.URL, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, r, job)
	}
}

func (api *Router) getYoutubeDownloadStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParamFromCtx(r.Context(), "id")
		job, err := api.ytImport.Status(id)
		if errors.Is(err, ytimport.ErrJobNotFound) {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, r, job)
	}
}

func (api *Router) applyYoutubeDownloadAlbum() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := chi.URLParamFromCtx(ctx, "id")

		var body struct {
			Album       string `json:"album"`
			AlbumArtist string `json:"albumArtist"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		job, err := api.ytImport.ApplyAlbum(ctx, id, body.Album, body.AlbumArtist)
		if err != nil {
			log.Warn(ctx, "Could not apply album to YouTube download", "id", id, "album", body.Album, err)
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, ytimport.ErrJobNotFound):
				status = http.StatusNotFound
			case errors.Is(err, ytimport.ErrJobNotReady):
				status = http.StatusConflict
			case errors.Is(err, ytimport.ErrAlbumNotFound):
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, r, job)
	}
}

func (api *Router) confirmYoutubeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := chi.URLParamFromCtx(ctx, "id")

		var body struct {
			Tracks []ytimport.TrackConfirm `json:"tracks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if err := api.ytImport.Confirm(ctx, id, body.Tracks); err != nil {
			log.Error(ctx, "Error confirming YouTube download", "id", id, err)
			status := http.StatusInternalServerError
			if errors.Is(err, ytimport.ErrJobNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, ytimport.ErrJobNotReady) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (api *Router) getYoutubeDownloadCover() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParamFromCtx(r.Context(), "id")
		trackID := chi.URLParamFromCtx(r.Context(), "trackId")

		img, err := api.ytImport.Cover(id, trackID)
		switch {
		case errors.Is(err, ytimport.ErrJobNotFound), errors.Is(err, ytimport.ErrJobHasNoFile),
			errors.Is(err, ytimport.ErrNoCoverArt):
			http.Error(w, "no cover art", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", http.DetectContentType(img))
		w.Header().Set("Cache-Control", "private, max-age=3600")
		if _, err := w.Write(img); err != nil {
			log.Error(r.Context(), "Error sending YouTube download cover art to client", err)
		}
	}
}

func (api *Router) setYoutubeDownloadCover() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParamFromCtx(r.Context(), "id")
		trackID := chi.URLParamFromCtx(r.Context(), "trackId")

		r.Body = http.MaxBytesReader(w, r.Body, maxCoverUploadSize)
		img, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "cover image too large or unreadable", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(http.DetectContentType(img), "image/") {
			http.Error(w, "uploaded file is not an image", http.StatusBadRequest)
			return
		}

		if err := api.ytImport.SetCover(id, trackID, img); err != nil {
			log.Error(r.Context(), "Error setting YouTube download cover art", "id", id, "trackId", trackID, err)
			status := http.StatusInternalServerError
			if errors.Is(err, ytimport.ErrJobNotFound) || errors.Is(err, ytimport.ErrJobHasNoFile) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (api *Router) setYoutubeDownloadAlbumCover() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParamFromCtx(r.Context(), "id")

		r.Body = http.MaxBytesReader(w, r.Body, maxCoverUploadSize)
		img, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "cover image too large or unreadable", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(http.DetectContentType(img), "image/") {
			http.Error(w, "uploaded file is not an image", http.StatusBadRequest)
			return
		}

		if err := api.ytImport.SetAlbumCover(id, img); err != nil {
			log.Error(r.Context(), "Error setting YouTube download album cover art", "id", id, err)
			status := http.StatusInternalServerError
			if errors.Is(err, ytimport.ErrJobNotFound) || errors.Is(err, ytimport.ErrJobHasNoFile) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (api *Router) rejectYoutubeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParamFromCtx(r.Context(), "id")
		if err := api.ytImport.Reject(id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ytimport.ErrJobNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, ytimport.ErrJobNotReady) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	response, err := json.Marshal(v)
	if err != nil {
		log.Error(r.Context(), "Error marshalling json", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(response); err != nil { //nolint:gosec
		log.Error(r.Context(), "Error sending response to client", err)
	}
}
