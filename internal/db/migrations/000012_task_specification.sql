-- Add specification field to tasks
ALTER TABLE tasks ADD COLUMN specification TEXT NOT NULL DEFAULT '';
