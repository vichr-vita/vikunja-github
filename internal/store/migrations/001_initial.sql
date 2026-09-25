CREATE TABLE IF NOT EXISTS webhook_deliveries (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 delivery_id TEXT NOT NULL UNIQUE,
 event_type TEXT NOT NULL,
 action TEXT NOT NULL DEFAULT '',
 payload BLOB NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('pending','processing','processed','failed')),
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 received_at INTEGER NOT NULL,
 processed_at INTEGER
);
CREATE INDEX IF NOT EXISTS deliveries_due ON webhook_deliveries(status,next_attempt_at,sequence);
CREATE TABLE IF NOT EXISTS github_references (
 id INTEGER PRIMARY KEY,
 task_id INTEGER NOT NULL,
 matched_task_ref TEXT NOT NULL DEFAULT '',
 repository_id INTEGER NOT NULL,
 repository_full_name TEXT NOT NULL,
 repository_url TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL CHECK(kind IN ('branch','commit','pull_request')),
 external_key TEXT NOT NULL,
 url TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT '',
 branch_name TEXT NOT NULL DEFAULT '',
 metadata_json TEXT NOT NULL DEFAULT '{}',
 occurred_at INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(task_id, repository_id, kind, external_key)
);
-- Records individual textual resolutions per GitHub object, including unresolved
-- references. This is deliberately not a global human-reference lookup cache.
CREATE TABLE IF NOT EXISTS object_resolutions (
 repository_id INTEGER NOT NULL,
 kind TEXT NOT NULL,
 external_key TEXT NOT NULL,
 task_ref TEXT NOT NULL,
 task_id INTEGER NOT NULL,
 PRIMARY KEY(repository_id,kind,external_key,task_ref)
);
CREATE TABLE IF NOT EXISTS branches (
 repository_id INTEGER NOT NULL,
 name TEXT NOT NULL,
 initialized INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL,
 sequence INTEGER NOT NULL,
 PRIMARY KEY(repository_id,name)
);
-- A SHA may be seen on several branches; preserve each comment membership.
CREATE TABLE IF NOT EXISTS reference_groups (
 reference_id INTEGER NOT NULL REFERENCES github_references(id) ON DELETE CASCADE,
 branch_name TEXT NOT NULL,
 PRIMARY KEY(reference_id,branch_name)
);
CREATE TABLE IF NOT EXISTS managed_comments (
 task_id INTEGER NOT NULL,
 repository_id INTEGER NOT NULL,
 branch_name TEXT NOT NULL,
 comment_id INTEGER NOT NULL DEFAULT 0,
 content_hash TEXT NOT NULL DEFAULT '',
 dirty INTEGER NOT NULL DEFAULT 1,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(task_id,repository_id,branch_name)
);
