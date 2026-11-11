package rag

import (
	"context"
	"strings"
)

// DocumentText reads stored chunks in order, removing their repeated overlap.
// Only the exact collection/filename is read; callers supply an authorized scope.
func (s *Store) DocumentText(ctx context.Context, collection, filename string) (string, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.content FROM rag_chunks c JOIN rag_documents d ON d.id=c.document_id WHERE d.collection=$1 AND d.filename=$2 ORDER BY c.chunk_index LIMIT 100`, collection, filename)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var text string
	for rows.Next() {
		var chunk string
		if err = rows.Scan(&chunk); err != nil {
			return "", err
		}
		overlap := 0
		left, right := []rune(text), []rune(chunk)
		for n := min(ChunkOverlap, min(len(left), len(right))); n > 0; n-- {
			if string(left[len(left)-n:]) == string(right[:n]) {
				overlap = n
				break
			}
		}
		if text != "" && overlap == 0 {
			text += "\n\n"
		}
		text += string(right[overlap:])
		if len(text) > 128<<10 {
			break
		}
	}
	return strings.TrimSpace(text), rows.Err()
}
