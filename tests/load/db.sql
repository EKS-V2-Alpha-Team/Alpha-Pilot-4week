\pset pager off

\echo '== Connections per service (budget: max_connections minus 3)'
SELECT application_name, state, count(*) FROM pg_stat_activity WHERE datname = 'orders' GROUP BY 1, 2 ORDER BY 3 DESC;
SELECT current_setting('max_connections') AS max_connections, count(*) AS in_use FROM pg_stat_activity;

\echo '== Sessions waiting on a lock'
SELECT application_name, wait_event_type, wait_event, count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' GROUP BY 1, 2, 3;
