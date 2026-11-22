package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAMessageWithAPictureIsSentAsParts(t *testing.T) {
	plain, _ := json.Marshal(Message{Role: "user", Content: "hi"})
	if string(plain) != `{"role":"user","content":"hi"}` {
		t.Errorf("plain message changed shape: %s", plain)
	}
	seen, _ := json.Marshal(Message{Role: "user", Content: "look", Images: []string{"data:image/jpeg;base64,AAAA"}})
	if !strings.Contains(string(seen), `"type":"image_url"`) || !strings.Contains(string(seen), `"text":"look"`) {
		t.Errorf("picture not sent as content parts: %s", seen)
	}
}
