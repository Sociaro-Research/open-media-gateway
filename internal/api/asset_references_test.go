package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRegisteredAssetInputReferences(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest("POST", "/v1/videos", nil)
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{`"asset://asset-character"`, []string{"asset://asset-character"}},
		{`{"image_url":"asset://asset-character"}`, []string{"asset://asset-character"}},
		{`["https://example.test/scene.png",{"image_url":"asset://asset-man"},"asset://asset-woman"]`, []string{"https://example.test/scene.png", "asset://asset-man", "asset://asset-woman"}},
	} {
		got, err := server.inputReferenceURLs(request, json.RawMessage(tc.raw))
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("got %v, %v; want %v", got, err, tc.want)
		}
	}
	if _, err := server.inputReferenceURLs(request, json.RawMessage(`{"file_id":"asset://asset-character"}`)); err == nil {
		t.Fatal("provider asset treated as Gateway file_id")
	}
}
