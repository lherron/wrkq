-- Unset shorts are NULL. The container store checks collisions with project
-- slugs inside the same write transaction as create, rename, and short update.
ALTER TABLE containers ADD COLUMN short TEXT;
CREATE UNIQUE INDEX idx_containers_short ON containers(short) WHERE short IS NOT NULL;
