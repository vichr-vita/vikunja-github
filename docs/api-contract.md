# Vikunja API contract

Inspected the target instance's `/api/v2/openapi.json` on 2026-09-25 before writing the client. Its `info.version` is `v2.6.0`. The client uses these operations:

| Operation | Request | Response |
| --- | --- | --- |
| Resolve reference | `GET /api/v2/projects/{project}/tasks/by-index/{index}` | `TaskReadOneBody`, with numeric `id` |
| Read task | `GET /api/v2/tasks/{projecttask}` | `TaskReadOneBody` |
| Find managed comment | `GET /api/v2/tasks/{task}/comments?format=markdown&page=N&per_page=100` | `PaginatedTaskComment`, with `items` and `total_pages` |
| Create comment | `POST /api/v2/tasks/{task}/comments?format=markdown` | HTTP 201, `TaskComment` |
| Update comment | `PATCH /api/v2/tasks/{task}/comments/{commentid}` | HTTP 200, `TaskComment` |

Comment writes send only `{"comment":"..."}`. PATCH uses `Content-Type: application/merge-patch+json` and `X-Vikunja-Format: markdown`. The specification explicitly warns that merge-patch drops query parameters. Bearer API tokens are supported through `APITokenAuth`.

The service never writes a task's completion state. API errors retain only HTTP status codes, not potentially sensitive response bodies. Redirects are rejected. Requests time out after 30 seconds.

A visible `vikunja-github:<sha256>` marker identifies each task/repository/branch comment. HTML comments may be removed by rich-text conversion, so they are not used. Recovery scans every comments page before issuing a POST when the local comment ID is absent. If a POST succeeds remotely but its response or subsequent SQLite write fails, retry finds and updates the existing comment. An uncertain POST is never followed by an immediate blind POST.

Only one service instance may own a database. The single worker prevents simultaneous comment creation within an instance. Running independent databases against the same tasks or multiple processes against one database is unsupported. Do not remove markers manually. Remote comment creation cannot be made atomic with SQLite; reconciliation depends on Vikunja returning newly created comments on subsequent reads.
