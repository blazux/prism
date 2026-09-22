package rag

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// PrepareEmbeddingIndex runs before serving RAG. Stored chunk text is retained;
// vectors and their identity change in one transaction, or not at all. Operators
// must stop other Prism processes using this database before a model migration.
func PrepareEmbeddingIndex(ctx context.Context, dsn string, embedder *Embedder, dim int, identity, legacyIdentity string, confirmed bool, progress func(int)) error {
	if dim < 1 || dim > 16000 || identity == "" {
		return errors.New("invalid embedding dimension or identity")
	}
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close(context.Background())
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS rag_embedding_meta (singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), identity text NOT NULL)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE rag_embedding_meta IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	old := legacyIdentity
	err = tx.QueryRow(ctx, `SELECT identity FROM rag_embedding_meta WHERE singleton=true`).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('rag_chunks') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if _, err = tx.Exec(ctx, `LOCK TABLE rag_chunks IN ACCESS EXCLUSIVE MODE`); err != nil {
			return err
		}
		var count int
		var oldDim int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM rag_chunks`).Scan(&count); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `SELECT atttypmod FROM pg_attribute WHERE attrelid='rag_chunks'::regclass AND attname='embedding'`).Scan(&oldDim); err != nil {
			return err
		}
		changed := old != identity || oldDim != dim
		if changed && count > 0 && !confirmed {
			return errors.New("embedding model differs from the document index; confirm rebuilding in AI provider settings, then restart")
		}
		if changed {
			if oldDim != dim {
				// NULL is transaction-local: old vectors return on any failure.
				if _, err = tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE rag_chunks ALTER COLUMN embedding TYPE vector(%d) USING NULL`, dim)); err != nil {
					return err
				}
			}
			var last int64
			done := 0
			for {
				rows, err := tx.Query(ctx, `SELECT id,content FROM rag_chunks WHERE id>$1 ORDER BY id LIMIT 128`, last)
				if err != nil {
					return err
				}
				ids := []int64{}
				texts := []string{}
				for rows.Next() {
					var id int64
					var content string
					if err = rows.Scan(&id, &content); err != nil {
						rows.Close()
						return err
					}
					ids = append(ids, id)
					texts = append(texts, content)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if len(ids) == 0 {
					break
				}
				vectors, err := embedder.EmbedBatch(ctx, texts)
				if err != nil {
					return errors.New("embedding rebuild failed; original index retained")
				}
				if len(vectors) != len(ids) {
					return errors.New("embedding response count mismatch; original index retained")
				}
				for i, id := range ids {
					if len(vectors[i]) != dim {
						return errors.New("embedding dimension changed during rebuild; original index retained")
					}
					for _, v := range vectors[i] {
						if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
							return errors.New("invalid embedding value")
						}
					}
					if _, err = tx.Exec(ctx, `UPDATE rag_chunks SET embedding=$1::text::vector WHERE id=$2`, pgvector.NewVector(vectors[i]).String(), id); err != nil {
						return err
					}
				}
				last = ids[len(ids)-1]
				done += len(ids)
				if progress != nil {
					progress(done)
				}
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO rag_embedding_meta(singleton,identity) VALUES(true,$1) ON CONFLICT(singleton) DO UPDATE SET identity=EXCLUDED.identity`, identity); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
