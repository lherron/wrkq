-- Workstreams and tasks allocate from the same sequence. Explicit imported IDs
-- and all existing rows retain their identities.
DROP TRIGGER tasks_ai_friendly;
CREATE TRIGGER tasks_ai_friendly
AFTER INSERT ON tasks
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_seq (id) VALUES (NULL);
  UPDATE tasks
     SET id = CASE WHEN NEW.kind = 'workstream' THEN 'W-' ELSE 'T-' END
              || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
