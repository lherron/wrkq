-- Migration: Add webhook URLs to containers
-- Adds webhook_urls JSON array for task webhook dispatch

ALTER TABLE containers ADD COLUMN webhook_urls TEXT;
