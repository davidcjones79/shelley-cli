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
			background: #1a1a2e;
			color: #eee;
			min-height: 100vh;
			margin: 0;
			display: flex;
			align-items: center;
			justify-content: center;
		}
		.dropzone {
			width: 80vw;
			max-width: 500px;
			height: 300px;
			border: 3px dashed #4a4a6a;
			border-radius: 20px;
			display: flex;
			flex-direction: column;
			align-items: center;
			justify-content: center;
			transition: all 0.2s;
			cursor: pointer;
		}
		.dropzone:hover, .dropzone.drag-over {
			border-color: #6c6cff;
			background: rgba(108, 108, 255, 0.1);
		}
		.dropzone h2 { margin: 0 0 10px 0; font-weight: 400; }
		.dropzone p { margin: 0; color: #888; font-size: 14px; }
		.result {
			margin-top: 20px;
			padding: 15px 20px;
			background: #2a2a4a;
			border-radius: 10px;
			font-family: monospace;
			font-size: 14px;
			cursor: pointer;
			display: none;
			white-space: pre-wrap;
			word-break: break-all;
		}
		.result:hover { background: #3a3a5a; }
		.result.show { display: block; }
		.copied { color: #6c6; }
		input[type="file"] { display: none; }
	</style>
</head>
<body>
	<div>
		<div class="dropzone" id="dropzone">
			<h2>📁 Drop files here</h2>
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
