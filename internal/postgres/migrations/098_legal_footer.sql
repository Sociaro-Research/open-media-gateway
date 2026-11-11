-- +goose Up
CREATE TABLE legal_footer (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    copyright_holder TEXT NOT NULL DEFAULT '' CHECK (octet_length(copyright_holder) <= 200),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO legal_footer(id) VALUES(TRUE);

-- +goose Down
DROP TABLE legal_footer;
