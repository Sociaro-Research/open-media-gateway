-- +goose Up
CREATE TABLE legal_documents (
    kind TEXT PRIMARY KEY CHECK (kind IN ('privacy', 'terms')),
    body TEXT NOT NULL DEFAULT '' CHECK (octet_length(body) <= 65536),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO legal_documents(kind) VALUES ('privacy'), ('terms');

-- +goose Down
DROP TABLE legal_documents;
