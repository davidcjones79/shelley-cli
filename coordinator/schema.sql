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
    status TEXT NOT NULL DEFAULT 'starting',  -- starting, idle, busy, offline
    current_task_id TEXT,
    tailscale_ip TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
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
    commit_sha TEXT,         -- final commit SHA
    pr_url TEXT,             -- PR URL if created
    pr_number INTEGER,       -- PR number if created
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

-- Indexes
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority DESC, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_workers_status ON workers(status);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_group ON tasks(group_id);
CREATE INDEX IF NOT EXISTS idx_task_groups_status ON task_groups(status);
