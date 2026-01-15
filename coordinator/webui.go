package coordinator

import (
	"embed"
	"net/http"
)

//go:embed dashboard.html
var dashboardHTML embed.FS

// HandleIndex serves the web UI.
func (c *Coordinator) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data, err := dashboardHTML.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "Dashboard not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}
