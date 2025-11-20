-- 000006_add_containers_archived_at.sql
-- Add archived_at column to containers table

ALTER TABLE containers ADD COLUMN archived_at TEXT;
