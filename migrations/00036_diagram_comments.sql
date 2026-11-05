-- +goose Up
-- Diagram comments (ported from the Node diagram server): a thread is a root
-- comment pinned to the canvas (anchor) plus flat replies. Notifications are
-- not a table of their own: they use the blog's notifications table, which
-- gets references to the diagram project, file and comment.
CREATE TABLE dg_comments (
    id          TEXT PRIMARY KEY,
    file_id     TEXT NOT NULL REFERENCES dg_files(id) ON DELETE CASCADE,
    project_id  TEXT NOT NULL REFERENCES dg_projects(id) ON DELETE CASCADE,
    -- NULL for a thread root; a root takes its replies with it
    parent_id   TEXT REFERENCES dg_comments(id) ON DELETE CASCADE,
    -- NULL once the account is gone ("Someone")
    author_id   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    body        TEXT NOT NULL,
    -- {"type":"point","x","y"} or {"type":"node","nodeId","dx","dy"}; NULL for a reply
    anchor      JSONB,
    resolved_at TIMESTAMPTZ,
    resolved_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX dg_comments_file_idx ON dg_comments (file_id, created_at, id);
CREATE INDEX dg_comments_project_idx ON dg_comments (project_id);
CREATE INDEX dg_comments_parent_idx ON dg_comments (parent_id) WHERE parent_id IS NOT NULL;

-- Members mentioned in a comment (not the author), as parsed at creation.
CREATE TABLE dg_comment_mentions (
    comment_id TEXT NOT NULL REFERENCES dg_comments(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (comment_id, user_id)
);
CREATE INDEX dg_comment_mentions_user_idx ON dg_comment_mentions (user_id);

-- When a user last opened a file's comments: later comments by others are unread.
CREATE TABLE dg_comment_reads (
    file_id TEXT NOT NULL REFERENCES dg_files(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (file_id, user_id)
);
CREATE INDEX dg_comment_reads_user_idx ON dg_comment_reads (user_id);

-- Diagram notifications (types diagram_mention, diagram_reply, diagram_comment)
-- live in the blog's table; text is the sentence shown for them. Deleting the
-- comment, file or project deletes its notifications.
ALTER TABLE notifications
    ADD COLUMN dg_project_id TEXT REFERENCES dg_projects(id) ON DELETE CASCADE,
    ADD COLUMN dg_file_id    TEXT REFERENCES dg_files(id) ON DELETE CASCADE,
    ADD COLUMN dg_comment_id TEXT REFERENCES dg_comments(id) ON DELETE CASCADE,
    ADD COLUMN text          TEXT;
CREATE INDEX notifications_dg_project_idx ON notifications (dg_project_id) WHERE dg_project_id IS NOT NULL;
CREATE INDEX notifications_dg_file_idx ON notifications (dg_file_id) WHERE dg_file_id IS NOT NULL;
CREATE INDEX notifications_dg_comment_idx ON notifications (dg_comment_id) WHERE dg_comment_id IS NOT NULL;

-- +goose Down
DROP INDEX notifications_dg_comment_idx;
DROP INDEX notifications_dg_file_idx;
DROP INDEX notifications_dg_project_idx;
DELETE FROM notifications WHERE type LIKE 'diagram\_%';
ALTER TABLE notifications
    DROP COLUMN text,
    DROP COLUMN dg_comment_id,
    DROP COLUMN dg_file_id,
    DROP COLUMN dg_project_id;
DROP TABLE dg_comment_reads;
DROP TABLE dg_comment_mentions;
DROP TABLE dg_comments;
