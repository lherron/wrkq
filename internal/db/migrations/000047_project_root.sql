-- Optional host-portable checkout root for top-level project containers.
-- Values are stored as caller-normalized strings (normally ~/...); consumers
-- expand them for the host on which they run.
ALTER TABLE containers ADD COLUMN root TEXT;
