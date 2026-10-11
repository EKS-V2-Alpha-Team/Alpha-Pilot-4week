\pset pager off
\set pattern lt- :run -%

\echo '== 1. Orders created by this run (must equal orders_created in the k6 summary)'
SELECT count(*) AS orders FROM orders WHERE customer_id LIKE :'pattern';

\echo '== 2. Final status (a declined payment ends cancelled, about 1 in 10)'
SELECT status, count(*) FROM orders WHERE customer_id LIKE :'pattern' GROUP BY status ORDER BY status;

\echo '== 3. Still pending (must be 0 once the queue has drained)'
SELECT count(*) AS pending FROM orders WHERE customer_id LIKE :'pattern' AND status = 'pending';

\echo '== 4. Seconds from order created to confirmed or cancelled'
SELECT count(*) AS orders,
       round(percentile_cont(0.50) WITHIN GROUP (ORDER BY secs)::numeric, 2) AS p50,
       round(percentile_cont(0.95) WITHIN GROUP (ORDER BY secs)::numeric, 2) AS p95,
       round(percentile_cont(0.99) WITHIN GROUP (ORDER BY secs)::numeric, 2) AS p99,
       round(max(secs)::numeric, 2) AS max,
       round(100.0 * count(*) FILTER (WHERE secs <= 10) / count(*), 2) AS pct_within_10s,
       round(100.0 * count(*) FILTER (WHERE secs <= 30) / count(*), 2) AS pct_within_30s
FROM (
  SELECT EXTRACT(EPOCH FROM MIN(e.created_at) - o.created_at) AS secs
  FROM orders o
  JOIN order_events e ON e.order_id = o.id AND e.event_type = 'status_changed' AND e.new_status IN ('confirmed', 'cancelled')
  WHERE o.customer_id LIKE :'pattern'
  GROUP BY o.id, o.created_at
) t;

\echo '== 5. Orders charged more than once (must return no rows)'
SELECT p.order_id, count(*) AS charges
FROM payments p JOIN orders o ON o.id = p.order_id
WHERE o.customer_id LIKE :'pattern' AND p.status = 'completed' AND p.method IS DISTINCT FROM 'refund'
GROUP BY p.order_id HAVING count(*) > 1;

\echo '== 6. Confirmed orders with no completed payment (must return no rows)'
SELECT o.id, o.status FROM orders o
WHERE o.customer_id LIKE :'pattern' AND o.status IN ('confirmed', 'processing', 'shipped', 'delivered')
  AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.order_id = o.id AND p.status = 'completed' AND p.method IS DISTINCT FROM 'refund');

\echo '== 7. Cancelled orders still holding stock (must return no rows once drained)'
SELECT r.order_id, r.status FROM reservations r JOIN orders o ON o.id = r.order_id
WHERE o.customer_id LIKE :'pattern' AND o.status = 'cancelled' AND r.status IN ('active', 'committed');

\echo '== 8. Stock that does not add up, all load-test products (must return no rows)'
SELECT p.id, p.stock, p.reserved, COALESCE(c.qty, 0) AS sold, COALESCE(a.qty, 0) AS held
FROM products p
LEFT JOIN (SELECT product_id, sum(quantity) AS qty FROM reservations WHERE status = 'committed' GROUP BY 1) c ON c.product_id = p.id
LEFT JOIN (SELECT product_id, sum(quantity) AS qty FROM reservations WHERE status = 'active' GROUP BY 1) a ON a.product_id = p.id
WHERE p.id LIKE 'lt-%'
  AND (p.stock < 0
       OR p.reserved < 0
       OR p.reserved > p.stock
       OR p.reserved <> COALESCE(a.qty, 0)
       OR p.stock + COALESCE(c.qty, 0) <> CASE WHEN p.id = 'lt-hot' THEN 1000000 ELSE 100000 END);

\echo '== 9. Events not yet delivered to the queue (must be 0 once drained)'
SELECT count(*) AS unsent,
       COALESCE(round(EXTRACT(EPOCH FROM now() - min(created_at))::numeric), 0) AS oldest_seconds
FROM order_outbox WHERE sent_at IS NULL;
