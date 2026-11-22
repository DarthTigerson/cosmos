package brain

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"cosmos/core/internal/store"
)

// Embedder turns texts into vectors. With one, she finds memories by
// what they mean, not only by the words they share with the message:
// "how is the little guy sleeping" finds what she knows about a baby.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// meaning holds the vectors she has, and the last message's vector so
// the several rankings of one turn ask the model once.
type meaning struct {
	mu        sync.Mutex
	model     string
	vecs      map[int64][]float32
	lastQuery string
	lastVec   []float32
}

// The model wants to be told which side of a search a text is on.
const (
	asQuery    = "search_query: "
	asDocument = "search_document: "
)

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// vectors returns the stored vectors for the model in use, loading them
// once.
func (b *Brain) vectors() map[int64][]float32 {
	model := b.Cfg.Get().Embed.Model
	b.mean.mu.Lock()
	defer b.mean.mu.Unlock()
	if b.mean.vecs == nil || b.mean.model != model {
		b.mean.vecs, _ = b.S.Embeddings(model)
		if b.mean.vecs == nil {
			b.mean.vecs = map[int64][]float32{}
		}
		b.mean.model = model
	}
	return b.mean.vecs
}

// queryVector is the vector of a message, or nil when she has no way of
// making one. It never holds a reply up for long.
func (b *Brain) queryVector(query string) []float32 {
	if b.E == nil || b.Cfg.Get().Embed.BaseURL == "" || query == "" {
		return nil
	}
	b.mean.mu.Lock()
	if b.mean.lastQuery == query {
		v := b.mean.lastVec
		b.mean.mu.Unlock()
		return v
	}
	b.mean.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := b.E.Embed(ctx, []string{asQuery + clip(query, 600)})
	if err != nil || len(out) != 1 {
		return nil
	}
	b.mean.mu.Lock()
	b.mean.lastQuery, b.mean.lastVec = query, out[0]
	b.mean.mu.Unlock()
	return out[0]
}

// rank picks the memories that bear on a message. Shared words still
// count, and so do importance and how recent a memory is; closeness in
// meaning counts most. Without an embedding model it is the plain
// word-matching rank.
func (b *Brain) rank(mems []store.Memory, query string, now time.Time, limit int) []store.Memory {
	if len(mems) <= limit {
		return mems
	}
	if pinned, rest := splitPinned(mems); len(pinned) > 0 {
		return withPinned(pinned, b.rank(rest, query, now, max(0, limit-len(pinned))), limit)
	}
	qv := b.queryVector(query)
	if qv == nil {
		return rankMemories(mems, query, now, limit)
	}
	vecs := b.vectors()
	terms := map[string]bool{}
	for _, t := range tokens(query) {
		terms[t] = true
	}
	type scored struct {
		m     store.Memory
		score float64
	}
	all := make([]scored, len(mems))
	for i, m := range mems {
		overlap := 0
		for _, t := range tokens(m.Category + " " + m.Content) {
			if terms[t] {
				overlap++
			}
		}
		score := float64(overlap) + float64(m.Importance)*0.15
		if v, ok := vecs[m.ID]; ok {
			score += cosine(qv, v) * 6
		}
		all[i] = scored{m, score}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	picked := make([]store.Memory, limit)
	for i := range picked {
		picked[i] = all[i].m
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].CreatedAt.Before(picked[j].CreatedAt) })
	return picked
}

// embedMissing gives a vector to memories that have none yet, a few at
// a time. It runs in the background after replies and on the heartbeat.
func (b *Brain) embedMissing(ctx context.Context) {
	if b.E == nil || b.Cfg.Get().Embed.BaseURL == "" {
		return
	}
	mems, _ := b.S.Memories()
	vecs := b.vectors()
	todo := []store.Memory{}
	for _, m := range mems {
		if _, ok := vecs[m.ID]; !ok {
			todo = append(todo, m)
			if len(todo) == 24 {
				break
			}
		}
	}
	if len(todo) == 0 {
		return
	}
	texts := make([]string, len(todo))
	for i, m := range todo {
		texts[i] = asDocument + clip(m.Content, 600)
	}
	out, err := b.E.Embed(ctx, texts)
	if err != nil {
		b.Log.Printf("embedding memories: %v", err)
		return
	}
	model := b.Cfg.Get().Embed.Model
	b.mean.mu.Lock()
	defer b.mean.mu.Unlock()
	for i, m := range todo {
		b.S.SetEmbedding(m.ID, model, out[i])
		if b.mean.vecs != nil && b.mean.model == model {
			b.mean.vecs[m.ID] = out[i]
		}
	}
}
