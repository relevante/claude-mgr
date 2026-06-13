package server

import "net/http"

// staticHandler serves the mobile web frontend. The real embedded single-page
// app lands in the frontend task; this placeholder keeps the server runnable and
// confirms the auth gate and routing in the meantime.
func (s *Server) staticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><meta name=viewport content="width=device-width,initial-scale=1"><title>claude-mgr</title><body style="font-family:system-ui;padding:1rem"><h1>claude-mgr</h1><p>Frontend pending. API is live: <code>/api/sessions</code></p>`))
	})
}
