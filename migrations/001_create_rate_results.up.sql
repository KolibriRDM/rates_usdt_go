CREATE TABLE IF NOT EXISTS rate_results (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    symbol TEXT NOT NULL CHECK (symbol <> ''),
    received_at TIMESTAMPTZ NOT NULL,
    ask NUMERIC NOT NULL CHECK (ask > 0),
    bid NUMERIC NOT NULL CHECK (bid > 0),
    method TEXT NOT NULL CHECK (method IN ('topN', 'avgNM')),
    n INTEGER NOT NULL CHECK (n >= 1),
    m INTEGER,
    CONSTRAINT rate_results_range_check CHECK (
        (method = 'topN' AND m IS NULL)
        OR (method = 'avgNM' AND m IS NOT NULL AND m >= n)
    )
);

CREATE INDEX IF NOT EXISTS idx_rate_results_received_at ON rate_results (received_at);
