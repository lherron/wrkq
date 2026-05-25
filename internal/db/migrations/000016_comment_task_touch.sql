-- Migration: touch parent tasks when comments change.
-- Task recency should reflect discussion activity, not only direct task edits.

DROP TRIGGER IF EXISTS comments_ai_touch_task;
DROP TRIGGER IF EXISTS comments_au_touch_task;
DROP TRIGGER IF EXISTS comments_ad_touch_task;

CREATE TRIGGER comments_ai_touch_task
AFTER INSERT ON comments
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = NEW.actor_uuid
   WHERE uuid = NEW.task_uuid;
END;

CREATE TRIGGER comments_au_touch_task
AFTER UPDATE ON comments
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.deleted_by_actor_uuid, NEW.actor_uuid)
   WHERE uuid = NEW.task_uuid;
END;

CREATE TRIGGER comments_ad_touch_task
AFTER DELETE ON comments
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = OLD.actor_uuid
   WHERE uuid = OLD.task_uuid;
END;
