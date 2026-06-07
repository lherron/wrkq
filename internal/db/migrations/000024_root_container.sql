-- Introduce a single path-invisible internal root container (kind='root') that
-- becomes the parent of every project. Webhooks registered on the root are
-- inherited by all projects via the existing container_chain CTE (collectWebhookURLs),
-- eliminating per-project webhook duplication. The root is EXCLUDED from
-- v_container_paths, so every existing path and ProjectScopeID stays byte-for-byte
-- unchanged. v_task_paths derives from v_container_paths and therefore also unchanged.
--
-- Ordering is critical: drop the old kind-check triggers BEFORE inserting
-- kind='root' and reparenting, and create the new invariants AFTER the data is in
-- its new shape, so no trigger can reject the transition itself.
--
-- Sentinel identities (UUID is the authority; kind='root' is the meaning; the
-- friendly id and slug are plumbing):
--   root container:  uuid 00000000-0000-4000-8000-000000000001  id P-00000  slug wrkq-system-root
--   system actor:    uuid 00000000-0000-4000-8000-0000000000a0  id A-00000  slug wrkq-system

-- 1. Drop old kind-check triggers (they reject any kind not in project/feature/area/misc)
--    and the old project-root triggers (they enforce project => parent_uuid IS NULL,
--    which the reparent in step 5 must violate). The new parent/kind consistency
--    triggers in step 7b supersede containers_project_root_*.
DROP TRIGGER IF EXISTS containers_kind_check_insert;
DROP TRIGGER IF EXISTS containers_kind_check_update;
DROP TRIGGER IF EXISTS containers_project_root_insert;
DROP TRIGGER IF EXISTS containers_project_root_update;

-- 2. Recreate kind-check triggers with 'root' permitted. The CLI still refuses
--    --kind root; the DB permits it only so bootstrap/migration can seed the root.
CREATE TRIGGER containers_kind_check_insert
BEFORE INSERT ON containers
WHEN NEW.kind NOT IN ('project', 'feature', 'area', 'misc', 'root')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, feature, area, misc, or root');
END;
CREATE TRIGGER containers_kind_check_update
BEFORE UPDATE OF kind ON containers
WHEN NEW.kind NOT IN ('project', 'feature', 'area', 'misc', 'root')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, feature, area, misc, or root');
END;

-- 3. Ensure the fixed internal system actor exists. Explicit non-empty id skips
--    the actors_ai_friendly trigger, so actor_seq / friendly ids do not drift.
--    OR IGNORE makes this safe on both fresh and already-seeded databases.
INSERT OR IGNORE INTO actors (uuid, id, slug, display_name, role)
VALUES ('00000000-0000-4000-8000-0000000000a0', 'A-00000', 'wrkq-system', 'wrkq system', 'system');

-- 4. Insert the root container. Explicit non-empty id skips containers_ai_friendly
--    so container_seq does not drift. If a project already holds the reserved slug,
--    containers_unique_root_slug makes this fail loudly (acceptable per design).
INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind,
                        created_by_actor_uuid, updated_by_actor_uuid)
VALUES ('00000000-0000-4000-8000-000000000001', 'P-00000', 'wrkq-system-root', 'wrkq root',
        NULL, 'root',
        '00000000-0000-4000-8000-0000000000a0', '00000000-0000-4000-8000-0000000000a0');

-- 5. Reparent every existing top-level project under the root. After this the
--    root is the only container with parent_uuid IS NULL.
UPDATE containers
   SET parent_uuid = '00000000-0000-4000-8000-000000000001'
 WHERE parent_uuid IS NULL
   AND uuid <> '00000000-0000-4000-8000-000000000001';

-- 6. Rebuild v_container_paths so children-of-root are level 0 (path = own slug)
--    and the root row never appears. Existing paths are preserved byte-for-byte.
DROP VIEW IF EXISTS v_container_paths;
CREATE VIEW v_container_paths AS
WITH RECURSIVE container_tree(uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, path, level) AS (
  SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, slug AS path, 0 AS level
    FROM containers
   WHERE parent_uuid = '00000000-0000-4000-8000-000000000001'
  UNION ALL
  SELECT c.uuid, c.id, c.slug, c.title, c.parent_uuid, c.kind, c.section_uuid, c.sort_index,
         ct.path || '/' || c.slug AS path,
         ct.level + 1 AS level
    FROM containers c
    JOIN container_tree ct ON c.parent_uuid = ct.uuid
)
SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, path, level
  FROM container_tree;

-- 7. Create the new invariants AFTER the data is in its new shape.

-- 7a. At-most-one root. (At-least-one is enforced by migration seeding above and
--     by the doctor exactly-one-root check; SQLite cannot express it row-locally.)
CREATE UNIQUE INDEX containers_single_root ON containers(kind) WHERE kind = 'root';

-- 7b. Parent/kind consistency. Subsumes the non-root-null guard and forbids
--     directories directly under root: top-level visible containers are projects only.
CREATE TRIGGER containers_parent_kind_consistency_insert
BEFORE INSERT ON containers
WHEN NOT (
  (NEW.kind = 'root'    AND NEW.parent_uuid IS NULL) OR
  (NEW.kind = 'project' AND NEW.parent_uuid = '00000000-0000-4000-8000-000000000001') OR
  (NEW.kind NOT IN ('root', 'project') AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid <> '00000000-0000-4000-8000-000000000001')
)
BEGIN
  SELECT RAISE(ABORT, 'container parent/kind invariant: root=>null parent, project=>parent=root, other=>non-root non-null parent');
END;
CREATE TRIGGER containers_parent_kind_consistency_update
BEFORE UPDATE OF kind, parent_uuid ON containers
WHEN NOT (
  (NEW.kind = 'root'    AND NEW.parent_uuid IS NULL) OR
  (NEW.kind = 'project' AND NEW.parent_uuid = '00000000-0000-4000-8000-000000000001') OR
  (NEW.kind NOT IN ('root', 'project') AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid <> '00000000-0000-4000-8000-000000000001')
)
BEGIN
  SELECT RAISE(ABORT, 'container parent/kind invariant violated');
END;

-- 7c. Root is immutable and undeletable through normal store/CLI paths.
CREATE TRIGGER containers_root_immutable_update
BEFORE UPDATE OF parent_uuid, slug, kind, archived_at ON containers
WHEN OLD.kind = 'root'
BEGIN
  SELECT RAISE(ABORT, 'root container is immutable (parent_uuid, slug, kind, archived_at)');
END;
CREATE TRIGGER containers_root_no_delete
BEFORE DELETE ON containers
WHEN OLD.kind = 'root'
BEGIN
  SELECT RAISE(ABORT, 'root container cannot be deleted');
END;

-- 7d. Tasks may never live directly under the root (DB-level so bundle/import/merge
--     paths are covered, not only the store layer).
CREATE TRIGGER tasks_not_under_root_insert
BEFORE INSERT ON tasks
WHEN NEW.project_uuid = '00000000-0000-4000-8000-000000000001'
BEGIN
  SELECT RAISE(ABORT, 'tasks cannot be created directly under the root container');
END;
CREATE TRIGGER tasks_not_under_root_update
BEFORE UPDATE OF project_uuid ON tasks
WHEN NEW.project_uuid = '00000000-0000-4000-8000-000000000001'
BEGIN
  SELECT RAISE(ABORT, 'tasks cannot be moved directly under the root container');
END;
