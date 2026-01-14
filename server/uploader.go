package server

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const uploaderHTMLTemplate = `<!DOCTYPE html>
<html>
<head>
	<title>Upload to {{.Hostname}}</title>
	<style>
		* { box-sizing: border-box; }
		body {
			font-family: -apple-system, system-ui, 'Segoe UI', sans-serif;
			background: #f5f5f5;
			color: #333;
			min-height: 100vh;
			margin: 0;
			padding: 0;
		}
		.navbar {
			position: fixed;
			top: 0;
			left: 0;
			right: 0;
			background: #fff;
			border-bottom: 1px solid #e0e0e0;
			padding: 12px 24px;
			display: flex;
			align-items: center;
			gap: 8px;
		}
		.navbar-logo {
			width: 28px;
			height: 28px;
		}
		.navbar-brand {
			font-size: 16px;
			font-weight: 600;
			color: #333;
		}
		.navbar-link {
			display: flex;
			align-items: center;
			gap: 8px;
			text-decoration: none;
		}
		.navbar-link:hover .navbar-brand {
			color: #666;
		}
		.main {
			min-height: 100vh;
			display: flex;
			align-items: center;
			justify-content: center;
			padding-top: 60px;
		}
		.container {
			background: #fff;
			border: 1px solid #e0e0e0;
			border-radius: 12px;
			padding: 40px;
			box-shadow: 0 1px 3px rgba(0,0,0,0.04);
			max-width: 480px;
		}
		.vm-header {
			display: flex;
			align-items: center;
			gap: 10px;
			margin-bottom: 24px;
		}
		.status {
			width: 10px;
			height: 10px;
			background: #22c55e;
			border-radius: 50%;
		}
		.hostname {
			font-family: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, monospace;
			font-size: 18px;
			font-weight: 500;
			color: #333;
			text-decoration: none;
		}
		.hostname:hover {
			color: #666;
			text-decoration: underline;
		}
		.dropzone {
			width: 100%;
			height: 180px;
			background: #fafafa;
			border: 2px dashed #d0d0d0;
			border-radius: 8px;
			display: flex;
			flex-direction: column;
			align-items: center;
			justify-content: center;
			gap: 8px;
			transition: all 0.15s;
			cursor: pointer;
		}
		.dropzone:hover, .dropzone.drag-over {
			border-color: #999;
			background: #f0f0f0;
		}
		.dropzone h2 {
			margin: 0;
			font-weight: 500;
			font-size: 16px;
			color: #333;
		}
		.dropzone p {
			margin: 0;
			color: #888;
			font-size: 14px;
		}
		.icon {
			margin-bottom: 8px;
		}
		.icon svg {
			width: 32px;
			height: 32px;
			stroke: #888;
			fill: none;
			stroke-width: 1.5;
		}
		.instructions {
			margin-top: 24px;
			padding: 16px;
			background: #f9fafb;
			border-radius: 8px;
			font-size: 14px;
			line-height: 1.5;
			color: #555;
		}
		.instructions-header {
			display: flex;
			justify-content: space-between;
			align-items: center;
			cursor: pointer;
		}
		.instructions h3 {
			margin: 0;
			font-size: 13px;
			font-weight: 600;
			color: #333;
			text-transform: uppercase;
			letter-spacing: 0.5px;
		}
		.instructions-arrow {
			width: 0;
			height: 0;
			border-left: 5px solid transparent;
			border-right: 5px solid transparent;
			border-top: 6px solid #888;
			transition: transform 0.2s;
		}
		.instructions.collapsed .instructions-arrow {
			transform: rotate(-90deg);
		}
		.instructions-body {
			margin-top: 12px;
		}
		.instructions.collapsed .instructions-body {
			display: none;
		}
		.instructions-body h4 {
			margin: 12px 0 8px 0;
			font-size: 12px;
			font-weight: 600;
			color: #555;
		}
		.instructions code {
			background: #e5e7eb;
			padding: 2px 6px;
			border-radius: 4px;
			font-family: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, monospace;
			font-size: 13px;
		}
		.instructions ol {
			margin: 0;
			padding-left: 20px;
		}
		.instructions li {
			margin-bottom: 4px;
		}
		.instructions p {
			margin: 0;
		}
		.instructions .supported {
			margin: 12px 0 0 0;
			font-size: 13px;
			color: #888;
		}
		.result {
			margin-top: 16px;
			padding: 12px 16px;
			background: #f0fdf4;
			border: 1px solid #bbf7d0;
			border-radius: 8px;
			font-family: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, monospace;
			font-size: 13px;
			cursor: pointer;
			display: none;
			white-space: pre-wrap;
			word-break: break-all;
			color: #166534;
		}
		.result:hover { background: #dcfce7; }
		.result.show { display: block; }
		.result-hint {
			font-size: 12px;
			color: #888;
			margin-top: 8px;
			display: none;
		}
		.result-hint.show { display: block; }
		input[type="file"] { display: none; }
		.upload-dir {
			margin-top: 16px;
			font-size: 12px;
			color: #888;
		}
		.upload-dir code {
			background: #f3f4f6;
			padding: 2px 6px;
			border-radius: 4px;
			font-family: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, monospace;
		}
		.recent {
			margin-top: 24px;
			padding-top: 24px;
			border-top: 1px solid #e0e0e0;
		}
		.recent-header {
			display: flex;
			justify-content: space-between;
			align-items: center;
			cursor: pointer;
			user-select: none;
		}
		.recent-toggle {
			display: flex;
			align-items: center;
			gap: 6px;
		}
		.recent h3 {
			margin: 0;
			font-size: 13px;
			font-weight: 600;
			color: #333;
			text-transform: uppercase;
			letter-spacing: 0.5px;
		}
		.recent-count {
			font-size: 11px;
			color: #888;
			background: #f0f0f0;
			padding: 2px 6px;
			border-radius: 10px;
		}
		.recent-arrow {
			font-size: 12px;
			width: 0;
			height: 0;
			border-left: 5px solid transparent;
			border-right: 5px solid transparent;
			border-top: 6px solid #888;
			transition: transform 0.2s;
		}
		.recent.collapsed .recent-arrow {
			transform: rotate(-90deg);
		}
		.recent-body {
			margin-top: 12px;
		}
		.recent.collapsed .recent-body {
			display: none;
		}
		.delete-all {
			font-size: 12px;
			color: #888;
			background: none;
			border: none;
			cursor: pointer;
			padding: 4px 8px;
			border-radius: 4px;
		}
		.delete-all:hover {
			background: #fee2e2;
			color: #dc2626;
		}
		.recent-list {
			list-style: none;
			margin: 0;
			padding: 0;
		}
		.recent-item {
			display: flex;
			align-items: center;
			gap: 10px;
			padding: 8px 0;
			border-bottom: 1px solid #f0f0f0;
		}
		.recent-item:last-child {
			border-bottom: none;
		}
		.recent-icon {
			font-size: 16px;
		}
		.recent-name {
			flex: 1;
			font-size: 13px;
			color: #333;
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
		.recent-meta {
			font-size: 12px;
			color: #888;
		}
		.recent-delete {
			width: 24px;
			height: 24px;
			border: none;
			background: none;
			color: #bbb;
			cursor: pointer;
			font-size: 16px;
			padding: 0;
			border-radius: 4px;
			display: flex;
			align-items: center;
			justify-content: center;
		}
		.recent-delete:hover {
			background: #fee2e2;
			color: #dc2626;
		}
		.recent-empty {
			font-size: 13px;
			color: #888;
		}
		.recent-thumb {
			width: 48px;
			height: 48px;
			object-fit: cover;
			border-radius: 6px;
			background: #f0f0f0;
			border: 1px solid #e0e0e0;
			cursor: pointer;
		}
		.recent-thumb:hover {
			opacity: 0.8;
		}
		/* Lightbox for full preview */
		.lightbox {
			position: fixed;
			top: 0;
			left: 0;
			right: 0;
			bottom: 0;
			background: rgba(0,0,0,0.9);
			display: none;
			align-items: center;
			justify-content: center;
			z-index: 1000;
			cursor: pointer;
		}
		.lightbox.show {
			display: flex;
		}
		.lightbox img {
			max-width: 90vw;
			max-height: 90vh;
			border-radius: 8px;
		}
	</style>
</head>
<body>
	<nav class="navbar">
		<a href="https://exe.dev/" class="navbar-link">
			<img class="navbar-logo" src="https://exe.dev/static/exy.png" alt="exe.dev">
			<span class="navbar-brand">exe.dev</span>
		</a>
	</nav>
	<div class="main">
	<div class="container">
		<div class="vm-header">
			<span class="status"></span>
			<a class="hostname" href="https://exe.dev/vm/{{.ShortHostname}}">{{.Hostname}}</a>
		</div>
		<div class="dropzone" id="dropzone">
			<span class="icon"><svg viewBox="0 0 24 24"><path d="M12 16V8m0 0l-3 3m3-3l3 3M3 16.5V18a2 2 0 002 2h14a2 2 0 002-2v-1.5M3 16.5l3.5-8A2 2 0 018.3 7h7.4a2 2 0 011.8 1.5l3.5 8" stroke-linecap="round" stroke-linejoin="round"/></svg></span>
			<h2>Drop files here</h2>
			<p>or click to browse</p>
		</div>
		<div class="result" id="result"></div>
		<div class="result-hint" id="result-hint">Click path to copy • Use <code>/pick</code> in Shelley CLI to analyze</div>
		<div class="instructions collapsed" id="instructions">
			<div class="instructions-header" onclick="toggleInstructions()">
				<h3>Help</h3>
				<span class="instructions-arrow"></span>
			</div>
			<div class="instructions-body">
				<p>Upload files from your local machine to your exe.dev VM. Perfect for sharing screenshots, images, documents, or code files with the Shelley AI agent.</p>
				<h4>How to use</h4>
				<ol>
					<li>Drag and drop files (or click to browse)</li>
					<li>In Shelley CLI, type <code>/pick</code> to list uploads</li>
					<li>Type <code>/pick 1</code> to analyze a file</li>
				</ol>
				<p class="supported">Supports images, CSV, JSON, Markdown, code files, and more.</p>
			</div>
		</div>
		<div class="upload-dir">Files saved to <code>{{.UploadDir}}</code></div>
		<div class="recent collapsed" id="recent">
			<div class="recent-header" onclick="toggleRecent()">
				<div class="recent-toggle">
					<h3>Recent uploads</h3>
					<span class="recent-count" id="recent-count">0</span>
				</div>
				<span class="recent-arrow"></span>
			</div>
			<div class="recent-body">
				<ul class="recent-list" id="recent-list"></ul>
				<button class="delete-all" id="delete-all" style="display:none" onclick="event.stopPropagation(); deleteAll()">Delete all</button>
			</div>
		</div>
	</div>
	</div>
	<input type="file" id="fileInput" multiple>
	<div class="lightbox" id="lightbox"><img id="lightbox-img" src=""></div>
	<script>
		const dropzone = document.getElementById('dropzone');
		const result = document.getElementById('result');
		const resultHint = document.getElementById('result-hint');
		const fileInput = document.getElementById('fileInput');

		dropzone.addEventListener('click', () => fileInput.click());
		fileInput.addEventListener('change', (e) => uploadFiles(e.target.files));

		dropzone.addEventListener('dragover', (e) => {
			e.preventDefault();
			dropzone.classList.add('drag-over');
		});
		dropzone.addEventListener('dragleave', () => dropzone.classList.remove('drag-over'));
		dropzone.addEventListener('drop', (e) => {
			e.preventDefault();
			dropzone.classList.remove('drag-over');
			uploadFiles(e.dataTransfer.files);
		});

		async function uploadFiles(files) {
			const paths = [];
			for (const file of files) {
				const formData = new FormData();
				formData.append('file', file);
				const resp = await fetch('/upload', { method: 'POST', body: formData });
				const path = await resp.text();
				paths.push(path);
			}
			result.textContent = paths.join('\n');
			result.classList.add('show');
			resultHint.classList.add('show');
			loadRecent();
		}

		result.addEventListener('click', () => {
			navigator.clipboard.writeText(result.textContent);
			const orig = result.textContent;
			result.textContent = 'Copied!';
			setTimeout(() => { result.textContent = orig; }, 1000);
		});

		// Recent uploads
		const recentList = document.getElementById('recent-list');
		const lightbox = document.getElementById('lightbox');
		const lightboxImg = document.getElementById('lightbox-img');

		lightbox.addEventListener('click', () => lightbox.classList.remove('show'));

		function showPreview(src) {
			lightboxImg.src = src;
			lightbox.classList.add('show');
		}

		async function deleteFile(name) {
			if (!confirm('Delete ' + name + '?')) return;
			try {
				await fetch('/file/' + encodeURIComponent(name), { method: 'DELETE' });
				loadRecent();
			} catch(e) {
				alert('Failed to delete');
			}
		}

		async function deleteAll() {
			if (!confirm('Delete all uploaded files?')) return;
			try {
				await fetch('/files', { method: 'DELETE' });
				loadRecent();
			} catch(e) {
				alert('Failed to delete');
			}
		}

		const deleteAllBtn = document.getElementById('delete-all');
		const recentSection = document.getElementById('recent');
		const recentCount = document.getElementById('recent-count');

		function toggleRecent() {
			recentSection.classList.toggle('collapsed');
		}

		function toggleInstructions() {
			document.getElementById('instructions').classList.toggle('collapsed');
		}

		async function loadRecent() {
			try {
				const resp = await fetch('/files');
				const files = await resp.json();
				recentCount.textContent = files.length;
				deleteAllBtn.style.display = files.length > 0 ? 'block' : 'none';
				if (files.length === 0) {
					recentList.innerHTML = '<li class="recent-empty">No uploads yet</li>';
					return;
				}
				recentList.innerHTML = files.map(f => {
					const icon = f.type === 'image' ? '🖼️' : f.type === 'csv' ? '📊' : f.type === 'code' ? '💻' : '📄';
					const fileUrl = '/file/' + encodeURIComponent(f.name);
					const thumb = f.type === 'image' 
						? '<img class="recent-thumb" src="' + fileUrl + '" onclick="showPreview(\'' + fileUrl + '\')">' 
						: '<span class="recent-icon">' + icon + '</span>';
					return '<li class="recent-item">' + thumb + '<span class="recent-name">' + f.name + '</span><span class="recent-meta">' + f.size + '</span><button class="recent-delete" onclick="deleteFile(\'' + f.name.replace(/'/g, "\\'") + '\')" title="Delete">&times;</button></li>';
				}).join('');
			} catch(e) {
				recentList.innerHTML = '<li class="recent-empty">Failed to load</li>';
			}
		}
		loadRecent();
	</script>
</body>
</html>`

// RunUploader starts the file upload server
func RunUploader(port int, uploadDir string) {
	// Get hostname for display
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "vm"
	}
	fullHostname := hostname + ".exe.xyz"

	// Parse template
	tmpl := template.Must(template.New("uploader").Parse(uploaderHTMLTemplate))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		tmpl.Execute(w, map[string]string{
			"Hostname":      fullHostname,
			"ShortHostname": hostname,
			"UploadDir":     uploadDir,
		})
	})

	http.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Timestamp prefix to avoid collisions
		filename := fmt.Sprintf("%d_%s", time.Now().Unix(), header.Filename)
		path := filepath.Join(uploadDir, filename)

		out, err := os.Create(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer out.Close()

		io.Copy(out, file)
		fmt.Fprint(w, path)
	})

	// List recent files as JSON, or DELETE all
	http.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			entries, err := os.ReadDir(uploadDir)
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() {
						os.Remove(filepath.Join(uploadDir, entry.Name()))
					}
				}
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		entries, err := os.ReadDir(uploadDir)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}

		type fileInfo struct {
			Name    string
			Size    string
			Type    string
			ModTime time.Time
		}
		var files []fileInfo

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			name := entry.Name()
			files = append(files, fileInfo{
				Name:    name,
				Size:    formatFileSize(info.Size()),
				Type:    detectType(name),
				ModTime: info.ModTime(),
			})
		}

		// Sort by modification time (newest first)
		for i := 0; i < len(files)-1; i++ {
			for j := i + 1; j < len(files); j++ {
				if files[j].ModTime.After(files[i].ModTime) {
					files[i], files[j] = files[j], files[i]
				}
			}
		}

		// Limit to 5 most recent
		if len(files) > 5 {
			files = files[:5]
		}

		w.Header().Set("Content-Type", "application/json")
		out := "["
		for i, f := range files {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf(`{"name":%q,"size":%q,"type":%q}`, f.Name, f.Size, f.Type)
		}
		out += "]"
		w.Write([]byte(out))
	})

	// Serve individual files (for thumbnails) and handle DELETE
	http.HandleFunc("/file/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/file/"):]
		if name == "" || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		filePath := filepath.Join(uploadDir, name)

		if r.Method == "DELETE" {
			if err := os.Remove(filePath); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		http.ServeFile(w, r, filePath)
	})

	http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}

func formatFileSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func detectType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return "image"
	case ".csv":
		return "csv"
	case ".go", ".py", ".js", ".ts", ".rs", ".c", ".cpp", ".java":
		return "code"
	default:
		return "file"
	}
}
