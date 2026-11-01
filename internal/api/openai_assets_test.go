package api

import (
	"net/http/httptest"
	"testing"
)

func TestLegacyProviderLogosAreAbsent(t *testing.T) {
	h := New(serviceStub{}, healthStub{}, "secret")
	for _, id := range []string{"openai", "agnes", "alibaba", "bytedance", "sonilo"} {
		for _, ext := range []string{"svg", "png"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/provider-assets/"+id+"/icon."+ext, nil))
			if w.Code != 404 {
				t.Fatal(id, ext, w.Code)
			}
		}
	}
}
