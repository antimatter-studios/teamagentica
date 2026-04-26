package server

import (
	"fmt"
	"net/http"

	"github.com/antimatter-studios/teamagentica/plugins/system-web-dashboard/internal/handlers"
)

// NewAPIHandler returns the HTTP handler for the API (mTLS) port.
// /schema and /events are auto-mounted by the SDK ListenAndServe helper.
func NewAPIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fetch", handlers.FetchHandler)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	return mux
}
