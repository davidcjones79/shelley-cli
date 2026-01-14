package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const uploaderHTML = `<!DOCTYPE html>
<html>
<head>
	<title>Upload to VM</title>
	<style>
		* { box-sizing: border-box; }
		body {
			font-family: -apple-system, system-ui, sans-serif;
			background: #0f1629;
			color: #eee;
			min-height: 100vh;
			margin: 0;
			display: flex;
			align-items: center;
			justify-content: center;
		}
		.dropzone {
			width: 90vw;
			max-width: 800px;
			height: 400px;
			background: #1a2744;
			border: 3px dashed #5b6eae;
			border-radius: 24px;
			display: flex;
			flex-direction: column;
			align-items: center;
			justify-content: center;
			gap: 12px;
			transition: all 0.2s;
			cursor: pointer;
		}
		.dropzone:hover, .dropzone.drag-over {
			border-color: #7b8eee;
			background: #1e2d52;
		}
		.dropzone h2 {
			margin: 0;
			font-weight: 500;
			font-size: 28px;
			color: #fff;
		}
		.dropzone p {
			margin: 0;
			color: #8899bb;
			font-size: 18px;
		}
		.icon {
			font-size: 32px;
			margin-bottom: 8px;
		}
		.result {
			margin-top: 24px;
			padding: 16px 24px;
			background: #1a2744;
			border-radius: 12px;
			font-family: monospace;
			font-size: 14px;
			cursor: pointer;
			display: none;
			white-space: pre-wrap;
			word-break: break-all;
			border: 1px solid #2a3754;
		}
		.result:hover { background: #1e2d52; }
		.result.show { display: block; }
		.copied { color: #6c6; }
		input[type="file"] { display: none; }
	</style>
</head>
<body>
	<div>
		<div class="dropzone" id="dropzone">
			<span class="icon">📁</span>
			<h2>Drop files here</h2>
			<p>or click to browse</p>
		</div>
		<div class="result" id="result"></div>
	</div>
	<input type="file" id="fileInput" multiple>
	<script>
		const dropzone = document.getElementById('dropzone');
		const result = document.getElementById('result');
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
		}

		result.addEventListener('click', () => {
			navigator.clipboard.writeText(result.textContent);
			result.classList.add('copied');
			setTimeout(() => result.classList.remove('copied'), 500);
		});
	</script>
</body>
</html>`

// RunUploader starts the file upload server
func RunUploader(port int, uploadDir string) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(uploaderHTML))
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
