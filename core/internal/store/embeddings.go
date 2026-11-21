package store

import (
	"encoding/binary"
	"math"
)

// SetEmbedding keeps the vector for a memory, as made by one model.
func (s *Store) SetEmbedding(memoryID int64, model string, vec []float32) error {
	raw := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(v))
	}
	_, err := s.db.Exec(`INSERT INTO embeddings(memory_id,model,vec) VALUES(?,?,?)
		ON CONFLICT(memory_id) DO UPDATE SET model=excluded.model, vec=excluded.vec`, memoryID, model, raw)
	return err
}

// Embeddings returns every stored vector made by this model, by memory.
func (s *Store) Embeddings(model string) (map[int64][]float32, error) {
	rows, err := s.db.Query(`SELECT memory_id, vec FROM embeddings WHERE model=?`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]float32{}
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		vec := make([]float32, len(raw)/4)
		for i := range vec {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		}
		out[id] = vec
	}
	return out, rows.Err()
}
