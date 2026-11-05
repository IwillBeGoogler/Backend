-- +goose Up
-- Claps and bookmarks on published diagrams, like a post's likes (1-50 claps
-- per reader) and bookmarks. Own tables: the blog's are keyed by post id, and
-- a file id is text. Deleting a file or an account deletes its rows.
CREATE TABLE dg_claps (
    file_id    TEXT NOT NULL REFERENCES dg_files (id) ON DELETE CASCADE,
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    count      INT NOT NULL DEFAULT 1 CHECK (count BETWEEN 1 AND 50),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (file_id, user_id)
);
CREATE INDEX dg_claps_user_idx ON dg_claps (user_id);

CREATE TABLE dg_bookmarks (
    file_id    TEXT NOT NULL REFERENCES dg_files (id) ON DELETE CASCADE,
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (file_id, user_id)
);
CREATE INDEX dg_bookmarks_user_idx ON dg_bookmarks (user_id, created_at DESC);

-- +goose Down
DROP TABLE dg_bookmarks;
DROP TABLE dg_claps;
