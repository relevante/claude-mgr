package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var webFS embed.FS

// staticHandler serves the embedded mobile web app. Static assets carry no
// secrets and the browser can't attach the bearer token when fetching them
// (e.g. <script src="app.js">), so they are NOT auth-gated — only /api/* is. The
// token travels in the page URL (?token=…) and the app uses it for API calls.
func (s *Server) staticHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "frontend unavailable", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	// Assets are embedded and change on every redeploy; tell the browser to
	// always revalidate so the phone never runs a stale app.js after an update.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}
