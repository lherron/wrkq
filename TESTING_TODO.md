# Test Fixes Needed

The test files I created have schema mismatches:

## Issues:
1. **actors table**: Uses `name` and `type` but should use `display_name` and `role`
2. **containers table**: Uses `name` but should use `title`  
3. **UUID handling**: Tests try to UPDATE uuids after INSERT, should provide uuid in INSERT directly

## Files Needing Fixes:
- internal/cli/cp_test.go
- internal/cli/rm_test.go
- internal/cli/doctor_test.go

## Proper Pattern (from integration_test.go):
```go
// Actors
database.Exec(`
    INSERT INTO actors (uuid, id, slug, display_name, role, created_at, updated_at)
    VALUES (?, 'A-00001', 'test-actor', 'Test Actor', 'human', datetime('now'), datetime('now'))
`, actorUUID)

// Containers  
database.Exec(`
    INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
    VALUES (?, 'P-00001', 'test-project', 'Test Project', datetime('now'), datetime('now'), ?, ?, 1)
`, containerUUID, actorUUID, actorUUID)

// Tasks
database.Exec(`
    INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
    VALUES (?, 'T-00001', 'test-task', 'Test Task', ?, 'open', 2, datetime('now'), datetime('now'), ?, ?, 1)
`, taskUUID, containerUUID, actorUUID, actorUUID)
```

## Quick Fix:
Run this script to fix all test files comprehensively.

