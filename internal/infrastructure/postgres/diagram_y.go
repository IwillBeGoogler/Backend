package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

// Yjs state of live-edited files (diagram.YRepository). Updates are opaque
// bytes: Postgres only orders them.

func (r *DiagramRepo) YLoad(ctx context.Context, fileID string) (diagram.YState, error) {
	var s diagram.YState
	err := pgx.BeginTxFunc(ctx, r.db, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT snapshot, seq FROM dg_file_ydocs WHERE file_id = $1`, fileID).Scan(&s.Snapshot, &s.SnapshotSeq)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT seq, data FROM dg_file_yupdates WHERE file_id = $1 AND seq > $2 ORDER BY seq`, fileID, s.SnapshotSeq)
		if err != nil {
			return err
		}
		s.Updates, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (u diagram.YUpdate, err error) {
			return u, row.Scan(&u.Seq, &u.Data)
		})
		return err
	})
	return s, err
}

// YAppend numbers the update one past the newest of the file (or of its
// snapshot, when compaction has dropped them all). An advisory lock on the
// file keeps two API instances from taking the same number.
func (r *DiagramRepo) YAppend(ctx context.Context, fileID string, data []byte) (int64, error) {
	var seq int64
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('dg_yupdates:' || $1::text, 0))`, fileID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO dg_file_yupdates (file_id, seq, data)
			SELECT f.id, GREATEST(
				COALESCE((SELECT max(seq) FROM dg_file_yupdates WHERE file_id = f.id), 0),
				COALESCE((SELECT seq FROM dg_file_ydocs WHERE file_id = f.id), 0)) + 1, $2::bytea
			FROM dg_files f WHERE f.id = $1::text
			RETURNING seq`, fileID, data).Scan(&seq)
	})
	return seq, notFound(err)
}

// YAppendMany numbers the updates one after the other inside one transaction
// (a write batch of the realtime hub), under the same advisory lock.
func (r *DiagramRepo) YAppendMany(ctx context.Context, fileID string, data [][]byte) (int64, error) {
	var seq int64
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('dg_yupdates:' || $1::text, 0))`, fileID); err != nil {
			return err
		}
		var next int64
		err := tx.QueryRow(ctx, `
			SELECT GREATEST(
				COALESCE((SELECT max(seq) FROM dg_file_yupdates WHERE file_id = f.id), 0),
				COALESCE((SELECT seq FROM dg_file_ydocs WHERE file_id = f.id), 0)) + 1
			FROM dg_files f WHERE f.id = $1::text`, fileID).Scan(&next)
		if err != nil {
			return err
		}
		for i, d := range data {
			if _, err := tx.Exec(ctx, `INSERT INTO dg_file_yupdates (file_id, seq, data) VALUES ($1::text, $2, $3::bytea)`, fileID, next+int64(i), d); err != nil {
				return err
			}
		}
		seq = next + int64(len(data)) - 1
		return nil
	})
	return seq, notFound(err)
}

func (r *DiagramRepo) YCompact(ctx context.Context, fileID string, snapshot []byte, seq int64) (bool, error) {
	done := false
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('dg_yupdates:' || $1::text, 0))`, fileID); err != nil {
			return err
		}
		var snapSeq, last int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT seq FROM dg_file_ydocs WHERE file_id = $1), 0),
			       GREATEST(COALESCE((SELECT max(seq) FROM dg_file_yupdates WHERE file_id = $1), 0),
			                COALESCE((SELECT seq FROM dg_file_ydocs WHERE file_id = $1), 0))`, fileID).Scan(&snapSeq, &last); err != nil {
			return err
		}
		if seq <= snapSeq || seq > last {
			return nil
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO dg_file_ydocs (file_id, snapshot, seq) SELECT id, $2::bytea, $3::bigint FROM dg_files WHERE id = $1::text
			ON CONFLICT (file_id) DO UPDATE SET snapshot = EXCLUDED.snapshot, seq = EXCLUDED.seq, updated_at = now()`,
			fileID, snapshot, seq)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM dg_file_yupdates WHERE file_id = $1 AND seq <= $2`, fileID, seq); err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, err
}

// YReset takes the same advisory lock as YAppend/YCompact, so a write batch
// that started before the reset lands before it (and is dropped with the rest).
func (r *DiagramRepo) YReset(ctx context.Context, fileID string) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('dg_yupdates:' || $1::text, 0))`, fileID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM dg_file_yupdates WHERE file_id = $1`, fileID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM dg_file_ydocs WHERE file_id = $1`, fileID)
		return err
	})
}

var _ diagram.YRepository = (*DiagramRepo)(nil)
