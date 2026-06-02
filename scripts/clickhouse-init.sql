CREATE TABLE IF NOT EXISTS default.dns_logs (
    timestamp     DateTime    NOT NULL,
    user_id       String      NOT NULL,
    client_ip     String      NOT NULL,
    domain        String      NOT NULL,
    query_type    String      NOT NULL,
    status        String      NOT NULL,
    response_time_ns UInt64  NOT NULL
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (timestamp, user_id)
TTL timestamp + INTERVAL 90 DAY DELETE;
