-- Skene Events Table
-- This table stores the event history for all workflow runs.

CREATE TABLE IF NOT EXISTS skene_events (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    sequence BIGINT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    type TEXT NOT NULL,
    step_name TEXT,
    data JSONB,
    output JSONB,
    metadata JSONB,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Ensure sequence is unique per run
    CONSTRAINT skene_events_run_sequence UNIQUE (run_id, sequence)
);

-- Index for loading events by run_id
CREATE INDEX IF NOT EXISTS idx_skene_events_run_id ON skene_events (run_id, sequence);

-- Index for loading events since a sequence
CREATE INDEX IF NOT EXISTS idx_skene_events_run_id_sequence ON skene_events (run_id, sequence) WHERE sequence > 0;
