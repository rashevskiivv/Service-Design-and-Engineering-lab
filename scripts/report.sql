-- Aggregate usage report for the slides: no key names, no content.
--
--   sqlite3 -readonly -header -column data/gateway.db < scripts/report.sql
--
-- Run it on a copy of the database if the gateway is busy. Edit the window
-- below (UTC) to cover only the lab session (default: everything).
-- Timestamps in the usage table are unix milliseconds.

CREATE TEMP VIEW w AS
SELECT * FROM usage
WHERE ts >= CAST(strftime('%s', '2000-01-01 00:00:00') AS INTEGER) * 1000
  AND ts <  CAST(strftime('%s', '2100-01-01 00:00:00') AS INTEGER) * 1000;

.print '== Overview'
SELECT COUNT(*)                                   AS requests,
       COUNT(DISTINCT key_id)                     AS active_keys,
       SUM(status = 200)                          AS ok,
       SUM(prompt_tokens)                         AS prompt_tokens,
       SUM(completion_tokens)                     AS completion_tokens,
       SUM(usage_estimated)                       AS estimated_rows,
       datetime(MIN(ts) / 1000, 'unixepoch')      AS first_utc,
       datetime(MAX(ts) / 1000, 'unixepoch')      AS last_utc
FROM w;

.print ''
.print '== Status histogram (499 = client disconnected)'
SELECT status, COALESCE(error_code, '') AS error_code, COUNT(*) AS requests
FROM w GROUP BY status, error_code ORDER BY status, requests DESC;

.print ''
.print '== Requests per endpoint and language'
SELECT endpoint, COALESCE(language, '-') AS language, COUNT(*) AS requests,
       SUM(status = 200) AS ok, SUM(stream) AS streamed,
       CAST(AVG(CASE WHEN status = 200 THEN completion_tokens END) AS INTEGER) AS avg_completion_tokens
FROM w GROUP BY endpoint, language ORDER BY requests DESC;

.print ''
.print '== Latency, TTFT and queue wait of successful requests (ms, nearest-rank percentiles)'
.print '   ttft_incl_queue: request arrival to first token, so it includes the queue wait'
WITH lat AS (
  SELECT endpoint, latency_ms AS v,
         ROW_NUMBER() OVER (PARTITION BY endpoint ORDER BY latency_ms) AS rn,
         COUNT(*)     OVER (PARTITION BY endpoint)                     AS n
  FROM w WHERE status = 200
), ttft AS (
  SELECT endpoint, ttft_ms AS v,
         ROW_NUMBER() OVER (PARTITION BY endpoint ORDER BY ttft_ms) AS rn,
         COUNT(*)     OVER (PARTITION BY endpoint)                  AS n
  FROM w WHERE status = 200 AND stream = 1 AND ttft_ms IS NOT NULL
), queue AS (
  SELECT endpoint, queue_ms AS v,
         ROW_NUMBER() OVER (PARTITION BY endpoint ORDER BY queue_ms) AS rn,
         COUNT(*)     OVER (PARTITION BY endpoint)                   AS n
  FROM w WHERE status = 200 AND queue_ms IS NOT NULL
)
SELECT 'latency' AS metric, endpoint, MAX(n) AS n,
       MIN(CASE WHEN rn >= 0.50 * n THEN v END) AS p50,
       MIN(CASE WHEN rn >= 0.95 * n THEN v END) AS p95,
       MIN(CASE WHEN rn >= 0.99 * n THEN v END) AS p99,
       MAX(v) AS max
FROM lat GROUP BY endpoint
UNION ALL
SELECT 'ttft_incl_queue', endpoint, MAX(n),
       MIN(CASE WHEN rn >= 0.50 * n THEN v END), MIN(CASE WHEN rn >= 0.95 * n THEN v END),
       MIN(CASE WHEN rn >= 0.99 * n THEN v END), MAX(v)
FROM ttft GROUP BY endpoint
UNION ALL
SELECT 'queue', endpoint, MAX(n),
       MIN(CASE WHEN rn >= 0.50 * n THEN v END), MIN(CASE WHEN rn >= 0.95 * n THEN v END),
       MIN(CASE WHEN rn >= 0.99 * n THEN v END), MAX(v)
FROM queue GROUP BY endpoint
ORDER BY metric, endpoint;

.print ''
.print '== Per-stream decode speed (completion tokens/s after the first token; p10 >= 10 is reading speed)'
WITH s AS (
  SELECT completion_tokens * 1000.0 / (latency_ms - ttft_ms) AS tps
  FROM w
  WHERE status = 200 AND stream = 1 AND ttft_ms IS NOT NULL AND latency_ms > ttft_ms AND completion_tokens > 1
), r AS (
  SELECT tps, ROW_NUMBER() OVER (ORDER BY tps) AS rn, COUNT(*) OVER () AS n FROM s
)
SELECT MAX(n) AS streams,
       ROUND(MIN(CASE WHEN rn >= 0.10 * n THEN tps END), 1) AS p10_tok_s,
       ROUND(MIN(CASE WHEN rn >= 0.50 * n THEN tps END), 1) AS p50_tok_s,
       ROUND(MAX(tps), 1)                                   AS max_tok_s
FROM r;

.print ''
.print '== Per minute (aggregate throughput)'
SELECT strftime('%Y-%m-%d %H:%M', ts / 1000, 'unixepoch') AS minute_utc,
       COUNT(*)                                             AS requests,
       SUM(status = 200)                                    AS ok,
       SUM(status IN (429, 503))                            AS limited,
       SUM(completion_tokens)                               AS completion_tokens,
       ROUND(SUM(completion_tokens) / 60.0, 1)              AS completion_tok_s
FROM w GROUP BY minute_utc ORDER BY minute_utc;
