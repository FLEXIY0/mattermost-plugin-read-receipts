package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// maxReadBodyBytes bounds the JSON body we are willing to buffer.
	maxReadBodyBytes = 4 << 10

	// maxStatusPostIDs bounds how many KV lookups one status request can cause.
	// Extra IDs are ignored; the webapp re-hydrates what is still on screen.
	maxStatusPostIDs = 200
)

func (p *Plugin) initRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(p.requireLoggedIn)

	api := router.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/read", p.handleMarkRead).Methods(http.MethodPost)
	api.HandleFunc("/status", p.handleGetStatuses).Methods(http.MethodGet)

	return router
}

// requireLoggedIn relies on Mattermost stripping any client-supplied
// Mattermost-User-ID header and setting it only for authenticated requests.
func (p *Plugin) requireLoggedIn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !model.IsValidId(r.Header.Get(headerUserID)) {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Plugin) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	var req readRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReadBodyBytes)).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !model.IsValidId(req.PostID) {
		http.Error(w, "A valid post_id is required", http.StatusBadRequest)
		return
	}

	readerID := r.Header.Get(headerUserID)

	status, appErr := p.markRead(req.PostID, readerID)
	if appErr != nil {
		// Never surface the raw AppError: it can carry storage details and it
		// distinguishes "post missing" from "not allowed" for the caller.
		p.API.LogWarn("Failed to mark post as read", "post_id", req.PostID, "error", appErr.Error())
		http.Error(w, "Unable to record read receipt", http.StatusInternalServerError)
		return
	}

	if status == nil {
		// The post is unknown, not visible to this user, or is their own. All
		// of those look identical to the client on purpose.
		writeJSON(w, map[string]string{"status": "ignored"})
		return
	}

	writeJSON(w, statusResponse{
		PostID: status.PostID,
		Status: status.DerivedStatus(),
		ReadBy: status.ReadBy,
	})
}

func (p *Plugin) handleGetStatuses(w http.ResponseWriter, r *http.Request) {
	postIDsParam := r.URL.Query().Get("post_ids")
	if postIDsParam == "" {
		http.Error(w, "post_ids is required", http.StatusBadRequest)
		return
	}

	requesterID := r.Header.Get(headerUserID)

	postIDs := strings.Split(postIDsParam, ",")
	if len(postIDs) > maxStatusPostIDs {
		postIDs = postIDs[:maxStatusPostIDs]
	}

	results := make([]statusResponse, 0, len(postIDs))
	seen := make(map[string]struct{}, len(postIDs))

	for _, postID := range postIDs {
		postID = strings.TrimSpace(postID)
		if !model.IsValidId(postID) {
			continue
		}

		if _, duplicate := seen[postID]; duplicate {
			continue
		}
		seen[postID] = struct{}{}

		_, status, appErr := p.getStatus(postID)
		if appErr != nil {
			p.API.LogWarn("Failed to load post status", "post_id", postID, "error", appErr.Error())
			http.Error(w, "Unable to load statuses", http.StatusInternalServerError)
			return
		}

		// Receipts say who read a message and are only ever shown to its
		// author, so only the author may read them back. Without this check any
		// account could dump the reader list of any post ID it can guess.
		if status == nil || status.AuthorID != requesterID {
			continue
		}

		results = append(results, statusResponse{
			PostID: status.PostID,
			Status: status.DerivedStatus(),
			ReadBy: status.ReadBy,
		})
	}

	writeJSON(w, map[string]any{"statuses": results})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")

	if err := json.NewEncoder(w).Encode(value); err != nil {
		// The status line is already written at this point, so there is nothing
		// useful left to send to the client.
		return
	}
}
