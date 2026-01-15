package coordinator

import (
	"embed"
	"net/http"
)

//go:embed dashboard.html
var dashboardHTML embed.FS

// HandleIndex serves the web UI.
// Requires exe.dev authentication (X-Exedev-Userid header) for dashboard access.
func (c *Coordinator) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Require exe.dev authentication for dashboard
	userID := r.Header.Get("X-Exedev-Userid")
	if userID == "" {
		// Redirect to exe.dev login
		http.Redirect(w, r, "/__exe.dev/login?redirect=/", http.StatusFound)
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
