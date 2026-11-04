-- Extension readers are active before this migration. Retain wrkq-owned values
-- with their JSON types; only affected rows run the normal task update triggers.
UPDATE tasks
SET meta = (
    SELECT json_group_object(j.key, json(tasks.meta -> j.key))
    FROM json_each(tasks.meta) AS j
    WHERE j.key IN ('workflow', '_wrkq_archive_cascade')
)
WHERE EXISTS (
    SELECT 1 FROM json_each(tasks.meta) AS j
    WHERE j.key NOT IN ('workflow', '_wrkq_archive_cascade')
);
