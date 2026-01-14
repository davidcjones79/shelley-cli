package server

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
		.instructions h3 {
			margin: 0 0 8px 0;
			font-size: 13px;
			font-weight: 600;
			color: #333;
			text-transform: uppercase;
			letter-spacing: 0.5px;
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
			margin: 0 0 12px 0;
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
	</style>
</head>
<body>
	<nav class="navbar">
		<img class="navbar-logo" src="https://exe.dev/static/exy.png" alt="exe.dev">
		<span class="navbar-brand">exe.dev</span>
	</nav>
	<div class="main">
	<div class="container">
		<div class="vm-header">
			<span class="status"></span>
			<span class="hostname">{{.Hostname}}</span>
		</div>
		<div class="dropzone" id="dropzone">
			<span class="icon"><svg viewBox="0 0 24 24"><path d="M12 16V8m0 0l-3 3m3-3l3 3M3 16.5V18a2 2 0 002 2h14a2 2 0 002-2v-1.5M3 16.5l3.5-8A2 2 0 018.3 7h7.4a2 2 0 011.8 1.5l3.5 8" stroke-linecap="round" stroke-linejoin="round"/></svg></span>
			<h2>Drop files here</h2>
			<p>or click to browse</p>
		</div>
		<div class="result" id="result"></div>
		<div class="result-hint" id="result-hint">Click path to copy • Use <code>/pick</code> in Shelley CLI to analyze</div>
		<div class="instructions">
			<h3>About</h3>
			<p>Upload files from your local machine to your exe.dev VM. Perfect for sharing screenshots, images, documents, or code files with the Shelley AI agent running on your VM.</p>
			<h3>How to use</h3>
			<ol>
				<li>Drag and drop files (or click to browse)</li>
				<li>In Shelley CLI, type <code>/pick</code> to list your uploads</li>
				<li>Type <code>/pick 1</code> to have Shelley analyze the file</li>
			</ol>
			<p class="supported">Supports images, CSV, JSON, Markdown, code files, and more.</p>
		</div>
		<div class="upload-dir">Files saved to <code>{{.UploadDir}}</code></div>
	</div>
	</div>
	<input type="file" id="fileInput" multiple>
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
		}

		result.addEventListener('click', () => {
			navigator.clipboard.writeText(result.textContent);
			const orig = result.textContent;
			result.textContent = 'Copied!';
			setTimeout(() => { result.textContent = orig; }, 1000);
		});
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
			"Hostname":  fullHostname,
			"UploadDir": uploadDir,
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

	http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}
