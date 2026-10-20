package api

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/openai"
)

func TestOpenAIIconRemainsSourceSVG(t *testing.T) {
	handler := New(serviceStub{}, healthStub{}, "secret")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest("GET", "/provider-assets/openai/icon.svg", nil))
	if result.Code != 200 || result.Header().Get("Content-Type") != "image/svg+xml" || !bytes.Equal(result.Body.Bytes(), openai.Icon()) {
		t.Fatal(result.Code, result.Header())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest("GET", "/provider-assets/openai/icon.png", nil))
	if missing.Code != 404 {
		t.Fatal(missing.Code)
	}
}
