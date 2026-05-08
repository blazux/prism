package rag

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

// Store manages the pgvector knowledge base.
type Store struct {
	pool *pgxpool.Pool
	dim  int
}

// Document represents a single uploaded file tracked in rag_documents.
type Document struct {
	ID         int64     `json:"id"`
	Collection string    `json:"collection"`
	Filename   string    `json:"filename"`
	FileHash   string    `json:"file_hash"`
	ChunkCount int       `json:"chunk_count"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Collection is a named group of documents with aggregate stats.
type Collection struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	DocCount    int       `json:"doc_count"`
	ChunkCount  int       `json:"chunk_count"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SearchResult is one chunk returned by a similarity search.
type SearchResult struct {
	Content    string  `json:"content"`
	Filename   string  `json:"filename"`
	Collection string  `json:"collection"`
	ChunkIndex int     `json:"chunk_index"`
	Score      float64 `json:"score"`
}

// NewStore opens a pgxpool, registers pgvector types on every connection,
// and ensures the schema exists.
//
// The extension must exist before pgxvector.RegisterTypes can look up the
// vector OID, so we use a plain pool to create it first, then open the real
// pool with the AfterConnect hook.
func NewStore(ctx context.Context, connStr string, dim int) (*Store, error) {
	// Step 1 — plain pool: create the pgvector extension if needed.
	plain, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("pgxpool (plain): %w", err)
	}
	if err := plain.Ping(ctx); err != nil {
		plain.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if _, err := plain.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		plain.Close()
		return nil, fmt.Errorf("create extension vector: %w", err)
	}
	plain.Close()

	// Step 2 — real pool: now the vector type exists, register it on every conn.
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse postgres URL: %w", err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvector.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}

	s := &Store{pool: pool, dim: dim}
	if err := s.initSchema(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return s, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) initSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS rag_collections (
			name        TEXT PRIMARY KEY,
			description TEXT NOT NULL DEFAULT ''
		)`,

		`CREATE TABLE IF NOT EXISTS rag_documents (
			id          BIGSERIAL PRIMARY KEY,
			collection  TEXT NOT NULL,
			filename    TEXT NOT NULL,
			file_hash   TEXT NOT NULL,
			chunk_count INT NOT NULL DEFAULT 0,
			size_bytes  BIGINT NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(collection, filename)
		)`,

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS rag_chunks (
			id          BIGSERIAL PRIMARY KEY,
			document_id BIGINT NOT NULL REFERENCES rag_documents(id) ON DELETE CASCADE,
			collection  TEXT NOT NULL,
			chunk_index INT NOT NULL,
			content     TEXT NOT NULL,
			embedding   vector(%d)
		)`, s.dim),

		// HNSW and IVFFlat are limited to 2000 dims in pgvector.
		// For high-dim models (e.g. qwen3-embedding:8b = 4096 dims) we fall back
		// to an exact sequential scan via the <=> operator — fast enough for
		// personal knowledge bases with tens of thousands of chunks.
		`CREATE INDEX IF NOT EXISTS rag_chunks_collection_idx
			ON rag_chunks (collection)`,
	}

	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(40, len(stmt))], err)
		}
	}
	return nil
}

// UpsertDocument inserts or replaces a document and all its chunks atomically.
func (s *Store) UpsertDocument(ctx context.Context, collection, filename, fileHash string, sizeBytes int64, chunks []string, embeddings [][]float32) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var docID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO rag_documents (collection, filename, file_hash, chunk_count, size_bytes, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (collection, filename) DO UPDATE
			SET file_hash   = EXCLUDED.file_hash,
			    chunk_count = EXCLUDED.chunk_count,
			    size_bytes  = EXCLUDED.size_bytes,
			    updated_at  = NOW()
		RETURNING id
	`, collection, filename, fileHash, len(chunks), sizeBytes).Scan(&docID)
	if err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	// Delete old chunks (CASCADE would handle this too, but we want to re-use the document row)
	if _, err = tx.Exec(ctx, `DELETE FROM rag_chunks WHERE document_id = $1`, docID); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	// Bulk-insert new chunks
	for i, chunk := range chunks {
		vec := pgvector.NewVector(embeddings[i])
		if _, err = tx.Exec(ctx, `
			INSERT INTO rag_chunks (document_id, collection, chunk_index, content, embedding)
			VALUES ($1, $2, $3, $4, $5)
		`, docID, collection, i, chunk, vec); err != nil {
			return fmt.Errorf("insert chunk %d: %w", i, err)
		}
	}

	return tx.Commit(ctx)
}

// ListCollections returns all collections with aggregate stats and descriptions.
func (s *Store) ListCollections(ctx context.Context) ([]Collection, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
		    COALESCE(rc.name, d.collection)   AS name,
		    COALESCE(rc.description, '')       AS description,
		    COUNT(DISTINCT d.id)               AS doc_count,
		    COALESCE(SUM(d.chunk_count), 0)    AS chunk_count,
		    MAX(d.updated_at)                  AS updated_at
		FROM rag_documents d
		LEFT JOIN rag_collections rc ON rc.name = d.collection
		GROUP BY rc.name, d.collection, rc.description
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.Name, &c.Description, &c.DocCount, &c.ChunkCount, &c.UpdatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// SetCollectionDescription upserts the description for a collection.
func (s *Store) SetCollectionDescription(ctx context.Context, name, description string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO rag_collections (name, description)
		VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description
	`, name, description)
	return err
}

// DeleteCollection removes an entire collection (documents cascade, then meta row).
func (s *Store) DeleteCollection(ctx context.Context, collection string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM rag_documents WHERE collection = $1`, collection); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM rag_collections WHERE name = $1`, collection)
	return err
}

// ListDocuments returns all documents in a collection.
func (s *Store) ListDocuments(ctx context.Context, collection string) ([]Document, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, collection, filename, file_hash, chunk_count, size_bytes, created_at, updated_at
		FROM rag_documents
		WHERE collection = $1
		ORDER BY filename
	`, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.Collection, &d.Filename, &d.FileHash, &d.ChunkCount, &d.SizeBytes, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// DeleteDocument removes a document and all its chunks.
func (s *Store) DeleteDocument(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM rag_documents WHERE id = $1`, id)
	return err
}

// Search performs a cosine similarity search and returns the top-k chunks.
func (s *Store) Search(ctx context.Context, collection string, embedding []float32, limit int) ([]SearchResult, error) {
	vec := pgvector.NewVector(embedding)
	rows, err := s.pool.Query(ctx, `
		SELECT c.content, d.filename, c.collection, c.chunk_index,
		       1 - (c.embedding <=> $1) AS score
		FROM rag_chunks c
		JOIN rag_documents d ON d.id = c.document_id
		WHERE c.collection = $2
		ORDER BY c.embedding <=> $1
		LIMIT $3
	`, vec, collection, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Content, &r.Filename, &r.Collection, &r.ChunkIndex, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
