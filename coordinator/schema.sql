-- Task groups (for batching related tasks)
CREATE TABLE IF NOT EXISTS task_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    repo_url TEXT,           -- shared repo for all tasks in group
    base_branch TEXT,        -- shared base branch
    status TEXT NOT NULL DEFAULT 'pending',  -- pending, running, completed, failed
    tasks_total INTEGER DEFAULT 0,
    tasks_completed INTEGER DEFAULT 0,
    tasks_failed INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

-- Workers (long-lived pool)
CREATE TABLE IF NOT EXISTS workers (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'starting',  -- starting, idle, busy, draining, offline, deleted
    current_task_id TEXT,
    shelley_version TEXT,  -- commit hash of shelley-cli on worker
    ssh_pubkey TEXT,       -- worker's SSH public key (for cleanup on delete)
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    ready_at DATETIME,     -- when worker first reported version (fully provisioned)
    last_heartbeat DATETIME,
    tasks_completed INTEGER DEFAULT 0
);

-- Task queue
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    prompt TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',  -- queued, assigned, running, completed, failed
    priority INTEGER DEFAULT 0,
    worker_id TEXT,
    result TEXT,
    error TEXT,
    -- Git integration
    repo_url TEXT,           -- e.g. github.com/user/repo
    base_branch TEXT,        -- branch to base work on (default: main)
    branch_name TEXT,        -- branch created by worker: task-{id}
    worktree_path TEXT,      -- path to git worktree (for shared repos)
    commit_sha TEXT,         -- final commit SHA
    pr_url TEXT,             -- PR URL if created
    pr_number INTEGER,       -- PR number if created
    -- Execution tracking
    conversation_id TEXT,    -- shelley conversation ID for viewing
    source TEXT DEFAULT 'autonomous',  -- 'manual', 'autonomous', 'api'
    -- Retry tracking
    retry_count INTEGER DEFAULT 0,
    -- Input staging
    input_dir TEXT,          -- path to staged input files: ~/shared/source/<task-id>/
    -- Timestamps
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    assigned_at DATETIME,
    started_at DATETIME,
    completed_at DATETIME,
    group_id TEXT,           -- optional group this task belongs to
    FOREIGN KEY (worker_id) REFERENCES workers(id),
    FOREIGN KEY (group_id) REFERENCES task_groups(id)
);

-- Audit log
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    event_type TEXT NOT NULL,  -- task.queued, task.assigned, task.completed, worker.started, etc.
    task_id TEXT,
    worker_id TEXT,
    details TEXT  -- JSON metadata
);

-- Coordinator settings (for persistent config like API token)
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Task artifacts (files produced by tasks that can be retrieved)
CREATE TABLE IF NOT EXISTS task_artifacts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    worker_id TEXT NOT NULL,
    filename TEXT NOT NULL,
    path TEXT NOT NULL,          -- path on worker VM
    url TEXT NOT NULL,           -- https://worker.exe.xyz:8000/path
    size_bytes INTEGER,
    content_type TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (task_id) REFERENCES tasks(id)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority DESC, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_workers_status ON workers(status);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_group ON tasks(group_id);
CREATE INDEX IF NOT EXISTS idx_task_groups_status ON task_groups(status);
CREATE INDEX IF NOT EXISTS idx_task_artifacts_task ON task_artifacts(task_id);

-- Shared repositories (cloned once, used by all workers via worktrees)
CREATE TABLE IF NOT EXISTS shared_repos (
    id TEXT PRIMARY KEY,              -- repo identifier (e.g., "owner-repo")
    url TEXT NOT NULL,                -- git clone URL
    path TEXT NOT NULL,               -- local path: ~/shared/repos/<id>/
    default_branch TEXT DEFAULT 'main',
    last_fetched DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Index for repo lookups
CREATE INDEX IF NOT EXISTS idx_shared_repos_url ON shared_repos(url);
