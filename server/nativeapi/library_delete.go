package nativeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/navidrome/navidrome/core"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/utils/req"
)

// addLibraryDeleteRoute adds admin-only bulk-delete endpoints for tracks and albums.
// These are registered directly on r (not via a nested r.Route call) because "/song"
// and "/album" already have a GET-only sub-router mounted by api.R elsewhere; chi
// panics if a path is claimed by more than one r.Route call.
func (api *Router) addLibraryDeleteRoute(r chi.Router) {
	r.Delete("/song", deleteSongs(api.maintenance))
	r.Delete("/album", deleteAlbums(api.maintenance))
}

func deleteSongs(maintenance core.Maintenance) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deleteLibraryItems(w, r, "song", maintenance.DeleteMediaFiles)
	}
}

func deleteAlbums(maintenance core.Maintenance) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deleteLibraryItems(w, r, "album", maintenance.DeleteAlbums)
	}
}

func deleteLibraryItems(w http.ResponseWriter, r *http.Request, kind string, delete func(ctx context.Context, ids []string) error) {
	ctx := r.Context()
	ids, _ := req.Params(r).Strings("id")
	if len(ids) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no ids provided")
		return
	}

	if err := delete(ctx, ids); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		log.Error(ctx, "Error deleting "+kind+"s", "ids", ids, err)
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeDeleteManyResponse(w, r, ids)
}

// writeJSONError sends a JSON body with a "message" field. Plain http.Error() sends
// text/plain, which react-admin's fetchJson can't parse: it falls back to the generic
// HTTP status text and silently drops the actual error, e.g. "read-only file system"
// on an attempt to delete a track from the main (read-only) library.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
