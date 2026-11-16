-- +goose Up
-- All-time reading time per post, kept apart from analytics_events so the
-- event retention purge never shrinks it. read_sessions counts read_time
-- events, so read_seconds / read_sessions is the average session.
ALTER TABLE posts ADD COLUMN read_seconds BIGINT NOT NULL DEFAULT 0,
                  ADD COLUMN read_sessions INT NOT NULL DEFAULT 0;

UPDATE posts p SET read_seconds = a.s, read_sessions = a.n
FROM (SELECT post_id, sum(value)::bigint AS s, count(*) AS n FROM analytics_events
      WHERE name = 'read_time' AND post_id IS NOT NULL GROUP BY post_id) a
WHERE a.post_id = p.id;

-- +goose Down
ALTER TABLE posts DROP COLUMN read_seconds, DROP COLUMN read_sessions;
