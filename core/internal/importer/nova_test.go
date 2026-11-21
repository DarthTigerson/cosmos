package importer

import (
	"os"
	"path/filepath"
	"testing"

	"cosmos/core/internal/store"
)

func TestNovaImportIsRepeatable(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "diary.json"), []byte(`{
	  "memories":[{"id":"a","category":"work","content":"Works as a developer.","createdAt":1760000000000,"updatedAt":1760000500000},
	              {"id":"b","category":"work","content":"works as a  developer.","createdAt":1760000000001,"updatedAt":1760000000001}],
	  "journal":[{"id":"j","date":"2026-10-01","content":"A long talk about the move.","source":"nova","createdAt":1759300000000}]}`), 0o600)
	os.WriteFile(filepath.Join(dir, "humans.json"), []byte(`{"humans":[{"id":"h","name":"Sam","aliases":["Sammy"],"relationship":"close friend","notes":"Met at uni.","lastMentioned":"2026-10-30"}]}`), 0o600)

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	c, err := Nova(s, dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Memories != 1 || c.Journal != 1 || c.People != 1 || c.Skipped != 1 {
		t.Fatalf("first import: %+v", c)
	}
	mems, _ := s.Memories()
	if mems[0].CreatedAt.UnixMilli() != 1760000000000 || mems[0].UpdatedAt.UnixMilli() != 1760000500000 {
		t.Fatalf("original dates were not kept: %+v", mems[0])
	}
	if p, ok := s.FindPerson("sammy"); !ok || p.Closeness != 0.7 {
		t.Fatalf("person not imported with alias and closeness: %+v", p)
	}

	again, err := Nova(s, dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Memories+again.Journal+again.People != 0 {
		t.Fatalf("a second import must add nothing: %+v", again)
	}
}

func TestNovaImportWithMissingFilesIsEmpty(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	c, err := Nova(s, t.TempDir())
	if err != nil || c != (Counts{}) {
		t.Fatalf("want empty counts and no error, got %+v %v", c, err)
	}
}
