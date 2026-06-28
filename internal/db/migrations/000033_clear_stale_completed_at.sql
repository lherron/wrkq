UPDATE tasks
   SET completed_at = NULL
 WHERE state != 'completed'
   AND completed_at IS NOT NULL;
