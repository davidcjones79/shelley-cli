package coordinator

import "net/http"

// HandleIndex serves the web UI.
func (c *Coordinator) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(indexHTML))
}

const indexHTML = `<!DOCTYPE html>
<html>
<head>
	<title>Shelley Coordinator</title>
	<style>
		* { box-sizing: border-box; }
		body { font-family: system-ui, sans-serif; max-width: 1100px; margin: 0 auto; padding: 20px; background: #f5f5f5; }
		h1 { color: #333; margin-bottom: 5px; }
		.subtitle { color: #666; margin-bottom: 20px; }
		.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; }
		.card { background: white; border-radius: 8px; padding: 20px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
		.card h3 { margin-top: 0; color: #333; border-bottom: 1px solid #eee; padding-bottom: 10px; }
		.full-width { grid-column: 1 / -1; }
		.stats { display: flex; gap: 20px; flex-wrap: wrap; }
		.stat { text-align: center; padding: 15px 25px; background: #f8f9fa; border-radius: 8px; }
		.stat-value { font-size: 2em; font-weight: bold; color: #007bff; }
		.stat-label { font-size: 0.9em; color: #666; }
		textarea { width: 100%; height: 80px; margin: 10px 0; font-family: monospace; padding: 10px; border: 1px solid #ddd; border-radius: 4px; }
		input[type="text"], input[type="password"], input[type="number"] { padding: 8px 12px; border: 1px solid #ddd; border-radius: 4px; margin: 5px 5px 5px 0; }
		button { padding: 8px 16px; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer; margin: 5px 5px 5px 0; }
		button:hover { background: #0056b3; }
		button.success { background: #28a745; }
		.badge { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 0.8em; }
		.badge-queued { background: #ffc107; color: #000; }
		.badge-running, .badge-assigned { background: #17a2b8; color: #fff; }
		.badge-completed { background: #28a745; color: #fff; }
		.badge-failed { background: #dc3545; color: #fff; }
		.badge-idle { background: #6c757d; color: #fff; }
		.badge-busy { background: #007bff; color: #fff; }
		.badge-starting { background: #ffc107; color: #000; }
		table { width: 100%; border-collapse: collapse; font-size: 0.9em; }
		th, td { text-align: left; padding: 8px; border-bottom: 1px solid #eee; }
		th { background: #f8f9fa; }
		.auth-bar { background: #fff; padding: 10px 15px; border-radius: 8px; margin-bottom: 20px; display: flex; align-items: center; gap: 10px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
	</style>
</head>
<body>
	<h1>🚀 Shelley Coordinator</h1>
	<p class="subtitle">Distributed Task Queue</p>

	<div class="auth-bar">
		<span>🔑</span>
		<input type="password" id="token" placeholder="API Token">
		<button onclick="saveToken()">Save</button>
		<span id="authStatus"></span>
	</div>

	<div class="card full-width">
		<h3>📊 Stats</h3>
		<div class="stats" id="stats">Loading...</div>
	</div>

	<div class="grid">
		<div class="card">
			<h3>📝 Enqueue Task</h3>
			<textarea id="prompt" placeholder="Enter task prompt..."></textarea>
			<button onclick="enqueueTask()">Add to Queue</button>
		</div>

		<div class="card">
			<h3>👷 Workers</h3>
			<div style="margin-bottom: 10px;">
				<input type="number" id="workerCount" value="1" min="0" max="10" style="width: 60px;">
				<button class="success" onclick="scaleWorkers()">Scale</button>
			</div>
			<div id="workers">Loading...</div>
		</div>

		<div class="card full-width">
			<h3>📋 Recent Tasks</h3>
			<div id="tasks">Loading...</div>
		</div>
	</div>

	<script>
		let token = localStorage.getItem('coordToken') || '';
		document.getElementById('token').value = token;

		function saveToken() {
			token = document.getElementById('token').value;
			localStorage.setItem('coordToken', token);
			document.getElementById('authStatus').textContent = '✓ Saved';
			refresh();
		}

		function api(path, opts = {}) {
			opts.headers = { ...opts.headers, 'X-Coordinator-Token': token };
			return fetch(path, opts).then(r => r.json());
		}

		async function refresh() {
			try {
				const stats = await api('/api/stats');
				document.getElementById('stats').innerHTML = ` + "`" + `
					<div class="stat"><div class="stat-value">${stats.tasks?.queued || 0}</div><div class="stat-label">Queued</div></div>
					<div class="stat"><div class="stat-value">${stats.tasks?.running || 0}</div><div class="stat-label">Running</div></div>
					<div class="stat"><div class="stat-value">${stats.tasks?.completed || 0}</div><div class="stat-label">Completed</div></div>
					<div class="stat"><div class="stat-value">${stats.workers?.total || 0}</div><div class="stat-label">Workers</div></div>
				` + "`" + `;

				const workers = await api('/api/workers');
				document.getElementById('workers').innerHTML = workers?.length ? workers.map(w => ` + "`" + `
					<div style="padding: 5px 0; border-bottom: 1px solid #eee;">
						<strong>${w.id}</strong> <span class="badge badge-${w.status}">${w.status}</span>
						<div style="font-size: 0.8em; color: #666;">Tasks: ${w.tasks_completed || 0}</div>
					</div>
				` + "`" + `).join('') : '<em>No workers</em>';

				const tasks = await api('/api/tasks?limit=10');
				document.getElementById('tasks').innerHTML = tasks?.length ? ` + "`" + `<table>
					<tr><th>ID</th><th>Status</th><th>Prompt</th></tr>
					${tasks.map(t => ` + "`" + `<tr>
						<td>${t.id}</td>
						<td><span class="badge badge-${t.status}">${t.status}</span></td>
						<td>${t.prompt?.substring(0, 60)}${t.prompt?.length > 60 ? '...' : ''}</td>
					</tr>` + "`" + `).join('')}
				</table>` + "`" + ` : '<em>No tasks</em>';
			} catch (e) {
				console.error(e);
			}
		}

		async function enqueueTask() {
			const prompt = document.getElementById('prompt').value;
			if (!prompt) { alert('Prompt required'); return; }
			await api('/api/enqueue', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ prompt })
			});
			document.getElementById('prompt').value = '';
			refresh();
		}

		async function scaleWorkers() {
			const count = document.getElementById('workerCount').value;
			await api('/api/scale?workers=' + count, { method: 'POST' });
			refresh();
		}

		setInterval(refresh, 5000);
		refresh();
	</script>
</body>
</html>`
