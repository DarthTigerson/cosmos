package brain

// Books and podcasts are heavy work for the models. She does it while she
// sleeps, when nobody is waiting on her, unless the user asks for one
// right now. What was asked for now is kept by id under these keys.
const (
	booksNow    = "books_now"
	episodesNow = "episodes_now"
)

// askedNow reports whether the user asked for this one to be done now.
func (b *Brain) askedNow(key string, id int64) bool {
	ids := []int64{}
	b.S.GetJSON(key, &ids)
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// setNow marks one as asked for now, or takes the mark off.
func (b *Brain) setNow(key string, id int64, now bool) {
	ids := []int64{}
	b.S.GetJSON(key, &ids)
	kept := []int64{}
	for _, x := range ids {
		if x != id {
			kept = append(kept, x)
		}
	}
	if now {
		kept = append(kept, id)
	}
	b.S.SetJSON(key, kept)
}

// nightWork reports whether a book or an episode waits for her sleep.
func (b *Brain) nightWork() bool {
	if b.BookDir != "" && b.Cfg.Get().Books {
		if _, ok := b.nextBook(false); ok {
			return true
		}
	}
	if b.PodDir != "" && b.Cfg.Get().Podcasts {
		if _, ok := b.nextEpisode(false); ok {
			return true
		}
	}
	return false
}
