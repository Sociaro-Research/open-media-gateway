package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAbortedMediaStreamReachesHTTPServer(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatal("abort swallowed or transformed", recovered)
		}
	}()
	recoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}
