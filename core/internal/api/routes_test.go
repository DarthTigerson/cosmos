package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Every route is registered without two patterns claiming the same
// address, which would stop the core at start.
func TestRoutesDoNotClash(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatal(r)
		}
	}()
	(&Server{}).Handler()
}

// A picture that is not a data:image/ address is turned away before the
// turn starts.
func TestMessageRejectsPicturesThatAreNotImages(t *testing.T) {
	for _, body := range []string{
		`{"text":"look","images":["http://example.com/a.jpg"]}`,
		`{"text":"look","images":["data:image/jpeg;base64,AA","data:image/jpeg;base64,AA","data:image/jpeg;base64,AA","data:image/jpeg;base64,AA","data:image/jpeg;base64,AA"]}`,
	} {
		w := httptest.NewRecorder()
		(&Server{}).message(w, httptest.NewRequest("POST", "/api/message", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("%s: got %d", body, w.Code)
		}
	}
}
