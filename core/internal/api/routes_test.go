package api

import "testing"

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
