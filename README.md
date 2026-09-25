# vikunja-github

[![CI](https://github.com/vichr-vita/vikunja-github/actions/workflows/ci.yml/badge.svg?branch=dev&event=push)](https://github.com/vichr-vita/vikunja-github/actions/workflows/ci.yml)
[![Release](https://github.com/vichr-vita/vikunja-github/actions/workflows/release.yml/badge.svg?branch=main&event=push)](https://github.com/vichr-vita/vikunja-github/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/vichr-vita/vikunja-github)](https://github.com/vichr-vita/vikunja-github/releases/latest)

A self-hosted Go service that links GitHub branches, commits, and pull requests to Vikunja tasks. GitHub repository webhooks feed a durable SQLite worker. The worker resolves references such as `LEDGER-42` through Vikunja API v2 and maintains GitHub activity comments on the resulting numeric task IDs.

Vikunja needs no modifications. The service does not poll GitHub, synchronize issues, complete tasks, or create branches or pull requests.

## Setup

1. Create a Vikunja API token in the instance's settings. Use a dedicated integration user with access only to the intended projects. Grant task read, comment read, comment create, and comment update permissions. Comment reads are required for crash reconciliation. No task update or comment delete permission is needed. Token permission labels vary with Vikunja versions; inspect your instance's API token permission selector. The current target advertises API-token creation through `/api/v1/tokens`, while this service uses v2 for every task and comment operation.
2. Set the project's identifier, for example `LEDGER`. Use the task's per-project index, not its numeric database ID, in references.
3. Copy `.env.example` to `.env`, restrict its permissions with `chmod 600 .env`, and set the instance URL and token. The URL is the instance root, without `/api/v2`.
4. Generate a webhook secret with `openssl rand -hex 32`. Put the same value in `GITHUB_WEBHOOK_SECRET` and the GitHub webhook configuration. Do not commit `.env`.
5. Start the container:

   ```sh
   docker compose -f compose.example.yml up -d --build
   ```

   The example builds locally; no published image is assumed. Add the service and volume to your existing Compose stack when ready. Keep exactly one instance per database. A named volume persists associations, queued deliveries, and comment IDs.
6. Configure your reverse proxy to expose `POST /webhooks/github` over HTTPS. Preserve the request body and GitHub headers. The example binds port 8080 to localhost. A proxy in the same Docker network can instead route to `vikunja-github:8080`. Keep `/healthz` and `/readyz` private.
7. In the GitHub repository, open **Settings → Webhooks → Add webhook**. Enter `https://your-hook-host/webhooks/github`, select `application/json`, configure the secret, and keep SSL verification enabled. Select individual events:
   - Branch or tag creation.
   - Branch or tag deletion.
   - Pushes.
   - Pull requests.
8. Create `feat/LEDGER-42-test` for an existing task. Check GitHub's recent delivery for a 202 response, then open the Vikunja task. It should have a managed GitHub development comment. Commit a change without a task reference to verify inheritance, then open and merge a PR to verify state updates.

Tags are ignored. GitHub's initial ping is accepted without requiring Vikunja connectivity. Authenticated events from repositories outside an allowlist receive 200 and are discarded.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `VIKUNJA_BASE_URL` | Required | Instance root URL |
| `VIKUNJA_TOKEN` | Required | Bearer API token |
| `GITHUB_WEBHOOK_SECRET` | Required | HMAC-SHA256 webhook secret |
| `DATABASE_PATH` | `/data/vikunja-github.db` | SQLite file |
| `LISTEN_ADDR` | `:8080` | HTTP listener |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `TASK_REF_REGEX` | `(?i)([A-Z][A-Z0-9]*)-(d+)` | Go regular expression with exactly two capture groups: project and index |
| `MAX_RETRY_ATTEMPTS` | `10` | Maximum attempts per delivery |
| `WEBHOOK_RETENTION_DAYS` | `7` | Retention for processed raw payloads |
| `MAX_COMMITS_IN_COMMENT` | `10` | Recent commits displayed per comment |
| `ALLOWED_REPOSITORIES` | Empty | Comma-separated repository names, case-insensitive |
| `ALLOWED_PROJECT_IDENTIFIERS` | Empty | Comma-separated project identifiers, case-insensitive |

An empty allowlist permits all values. Project identifiers normalize to uppercase. Numeric indexes must be positive and fit in `int64`. Custom expressions must include `(?i)` if case-insensitive matching is desired. The default uses Go's ASCII word-boundary behavior.

## Association rules

A branch reference resolves once for that branch and repository. Stored branch associations use the returned numeric task ID. Moving the task between Vikunja projects does not redirect later commits to a different task with the old identifier.

Pushes establish branch associations if creation was missed. Commits inherit those canonical task IDs and add references explicitly present in their messages. PRs combine their title, body, head branch, and existing head-branch associations. A known branch's old textual identifier is not resolved again. Fork PRs consult their head repository's branch associations, never an identically named branch in the base repository.

Textual resolutions are persisted per GitHub object, including 404 outcomes. This prevents retries or repeated PR updates from redirecting a previously encountered reference after a task move. A new textual reference in a PR update is resolved when first encountered. A genuinely new GitHub object resolves references afresh. There is no permanent global reference cache.

Unknown references produce warnings and do not block other references. A 401 or 403 fails the delivery with an authentication diagnostic. A timeout, network error, HTTP 408, 429, or 5xx is retried with exponential backoff from one second to five minutes, up to the configured attempt limit.

Each task/repository/branch has one managed comment. Explicit commits for tasks not associated with their branch use a repository-level comment. Fork PRs also use repository-level comments in the base repository. Only a bounded number of commits is rendered; all associations remain in SQLite. Repeated SHAs have one canonical reference row and can belong to multiple comment groups. GitHub text is escaped and `@mentions` are broken to avoid unintended Vikunja notifications.

Deleted branches remain historical. Merged PRs remain visible and never complete a task. PR state uses GitHub's update timestamp so an older delivery cannot reopen a merged PR. Branch events do not carry an equivalent resource timestamp; receipt order is used, and an older queued retry cannot overwrite a newer branch state. GitHub events that arrive out of their original order cannot always be reconstructed.

## Durability and recovery

The webhook endpoint validates HMAC against the raw body, checks the delivery ID, and commits the event before returning 202. The same delivery ID returns 200. Invalid signatures return 401; invalid JSON or required event fields return 400. Payloads above 25 MiB return 413. Storage failures return 503 so the failure is visible in GitHub's delivery history.

SQLite uses WAL mode, `synchronous=FULL`, a busy timeout, and uniqueness constraints. One in-process worker performs network operations outside transactions. At startup it recovers interrupted `processing` deliveries. Pending deliveries resume at their persisted retry time. A storage bookkeeping failure stops the service rather than silently abandoning a claimed delivery; Compose restarts it and recovery runs again.

Comment content hashes avoid redundant PATCH requests. Each comment includes a stable visible marker. If the comment ID was not committed locally, retry scans all comment pages for that marker before creating a comment. This covers a crash or lost response after a successful POST. A manually deleted comment is reconciled and replaced when activity changes. See [the inspected API contract](docs/api-contract.md) and [its relevant OpenAPI subset](docs/vikunja-v2.openapi.json).

Successful raw payloads are cleared at startup and hourly once their retention expires. Delivery IDs and statuses remain as small tombstones so old redeliveries cannot bypass deduplication. Failed payloads remain for diagnosis and manual replay. Back up the database using SQLite's backup command, or stop the service before copying the database and its WAL. Do not copy a live database file alone.

To retry a failed delivery after fixing its cause, stop the service and run a parameterized equivalent of:

```sql
UPDATE webhook_deliveries
SET status = 'pending', attempts = 0, next_attempt_at = 0, last_error = ''
WHERE delivery_id = 'the-delivery-id' AND status = 'failed';
```

Restart the service. GitHub redelivery of an already persisted ID deliberately does not reset a failed delivery's attempt budget.

## Health and logs

`GET /healthz` reports process liveness. `GET /readyz` requires validated startup configuration, an initialized worker, and an accessible database. Neither checks GitHub or Vikunja connectivity. A temporary Vikunja outage does not trigger container health restarts.

Logs are JSON. Delivery completion, retries, and failures include the delivery ID, event, action, and attempt. Reference diagnostics include task references and object keys. Comment writes include canonical task and comment IDs. Tokens, secrets, Authorization headers, remote error bodies, and webhook bodies are not logged.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| GitHub receives 401 | Confirm the same secret at both ends and that the proxy preserves exact request bytes and `X-Hub-Signature-256`. |
| A task reference is unresolved | Confirm the project identifier and per-project index. Check project access and the project allowlist. A 404 is recorded for that GitHub object; test again with a new branch after correcting the reference. |
| Vikunja returns 401 or 403 | Check token expiration, task read and comment read/create/update permissions, project access, and comment authorship. Use the same integration user that created the comments. |
| A delivery fails | Inspect `delivery_failed` logs and `webhook_deliveries.last_error`. Correct the cause and reset that failed delivery as described above. |
| No events arrive | Check webhook subscriptions, public HTTPS reachability, repository allowlist, and GitHub's delivery history. |
| Database permission errors | The container runs as UID/GID 10001. A bind-mounted directory must be writable by that user, including creation of WAL and SHM files. Use the named volume example if possible. |
| Comments appear twice | Confirm only one integration process/database is managing these tasks and that markers were not removed. Preserve the database on redeploy. |

## Development

Go 1.26 and the pure-Go `modernc.org/sqlite` driver are used. Runtime dependencies are limited to SQLite and its transitive dependencies. HTTP, JSON, logging, matching, and HMAC use the standard library.

```sh
make build
make check
make image
```

`make check` runs vet and the race-enabled test suite. Tests use temporary SQLite files and mock HTTP servers. They cover extraction, signature fixtures, concurrent duplicate ingestion, normalized GitHub events, canonical inheritance, PR states, outage retries, restart recovery, comment reconciliation, bounded rendering, and API failures. They require no live credentials.

See the [live end-to-end test report](docs/end-to-end-test.md) for the real-instance exercise and deployment validation limits.

The [GitHub webhook payload documentation](https://docs.github.com/en/webhooks/webhook-events-and-payloads) defines the event fields. Signature tests include [GitHub's published HMAC fixture](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries).

## Limits

Run one replica. Increasing worker concurrency requires per-comment serialization and delivery leases before it is safe. The service processes the commits present in push payloads; it does not backfill truncated GitHub payloads, pre-existing branches, or missed deliveries. Large force pushes may therefore omit historical commits. Retained associations are historical, not a current reachability graph of the repository.

No frontend changes, GitHub App installation, OAuth UI, issue synchronization, two-way updates, or task completion automation are included.

## Branches and releases

The default branch is `dev`. Open feature and fix PRs against `dev`; CI runs tests, vet, the binary build, and the container build. Promote accumulated changes with a PR from `dev` to `main`. Merge that promotion with a merge commit so both long-lived branches retain shared history. Keep both branches after merging.

Every push to `main` runs release validation, publishes binary archives and checksums on GitHub, and publishes `ghcr.io/vichr-vita/vikunja-github` images for Linux amd64 and arm64. Images use the release tag and `latest`. The initial release is `v0.1.0`; subsequent versions follow conventional commits: breaking changes bump the major version, `feat:` bumps the minor version, and other changes bump the patch version. Retrying a tagged commit reuses its version.

Creating or merging a release does not deploy the service. Update your own Compose stack to use a published version when ready.
