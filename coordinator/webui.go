package coordinator

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"path/filepath"
)

//go:embed dashboard.html
var dashboardHTML embed.FS

//go:embed templates/*.html
var templatesFS embed.FS

var templates *template.Template

func init() {
	// Parse templates
	templates = template.Must(template.ParseFS(templatesFS, "templates/*.html"))
}

// PageData contains data passed to templates
type PageData struct {
	Title    string
	Page     string
	APIToken string
}

// HandleIndex serves the dashboard web UI.
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

	// Inject API token for authenticated users so dashboard auto-connects
	data = bytes.Replace(data,
		[]byte(`let token = localStorage.getItem('coordToken') || '';`),
		[]byte(`let token = localStorage.getItem('coordToken') || '`+c.config.APIToken+`';`),
		1)

	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

// HandleHelp serves the help documentation page.
func (c *Coordinator) HandleHelp(w http.ResponseWriter, r *http.Request) {
	// Require exe.dev authentication
	userID := r.Header.Get("X-Exedev-Userid")
	if userID == "" {
		http.Redirect(w, r, "/__exe.dev/login?redirect=/help", http.StatusFound)
		return
	}

	data := PageData{
		Title:    "Help",
		Page:     "help",
		APIToken: c.config.APIToken,
	}

	// First render help.html which defines content, then base.html
	tmpl := template.Must(template.ParseFS(templatesFS, 
		filepath.Join("templates", "base.html"),
		filepath.Join("templates", "help.html"),
	))

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}
