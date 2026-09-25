# Live end-to-end test

Completed on 2026-09-25 against the user's Vikunja v2.6.0 instance and `vichr-vita/wedding-site`.

## Test resources

- Vikunja project 11, “Svatební web”, now has the authorized identifier `WEDDING`.
- `WEDDING-11`, numeric task ID 180: GitHub integration end-to-end test.
- `WEDDING-12`, numeric task ID 181: GitHub integration cross-task test.
- [GitHub test PR 12](https://github.com/vichr-vita/wedding-site/pull/12) merged into a temporary test base branch. The repository's `main` SHA remained unchanged.
- Original branch comment ID 12, cross-task repository comment ID 13. Merging the commits into the unrelated test base also created the expected repository-level comment ID 14 for task 180.

## Results

| Check | Observed result |
| --- | --- |
| Real branch creation webhook | HTTP 202; `WEDDING-11` resolved to task 180; branch association and comment 12 created |
| Commit without a textual task reference | Inherited task 180 from the branch |
| Commit mentioning `WEDDING-12` | Associated with both tasks 180 and 181 |
| PR without a task reference in its title | Inherited task 180 from the head branch |
| Simulated Vikunja outage | A local substitute returned 503; GitHub still received HTTP 202 and the merge delivery remained pending |
| Readiness during outage | HTTP 200 |
| Restart after outage | Existing pending deliveries resumed; merge processing succeeded on attempt 6 |
| Comment reconciliation | Cleared local comment bookkeeping before restart; the worker recovered original comment 12 by its marker from real Vikunja Markdown responses |
| PR merge | Stored state and original managed comment changed to `merged` |
| Task completion | Both test tasks remained `done=false` |
| GitHub redelivery | Same delivery ID returned HTTP 200 without a new delivery row |
| Branch deletion | Stored association remained with state `deleted`; original comment showed deleted branch and merged PR |
| Final queue | All deliveries processed |

The temporary webhook and both test branches were removed. The tunnel, test listener, and outage simulator were stopped. The two test tasks and their comments remain available for inspection. No production deployment was changed.

The SQLite test database was backed up to ignored `data/e2e.db` in the workspace. It contains the real received deliveries and canonical associations. It is test evidence, not a production database.

## Local validation

`go test -race ./...` passed 57 tests, and `go vet ./...` passed. `make build` builds with `CGO_ENABLED=0`. The Compose configuration validates with placeholder environment values.

Docker verification subsequently passed after adding the user to the `docker` group. The existing agent session required `newgrp docker` to acquire that membership. The `vikunja-github:local` image builds successfully. A temporary container verified UID 10001, a read-only root filesystem, a writable SQLite volume, both health endpoints, authenticated webhook ingestion, duplicate handling, and delivery persistence across container restart. The test container and its anonymous volume were removed. A production container has not been started.
