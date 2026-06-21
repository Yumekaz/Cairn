package dashboard

import (
	"embed"
	"net/http"
)

//go:embed index.html index.css index.js
var Content embed.FS

// Handler returns an http.Handler that serves the embedded web assets.
func Handler() http.Handler {
	return http.FileServer(http.FS(Content))
}
