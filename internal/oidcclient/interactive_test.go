package oidcclient

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestCallbackHandlerIgnoresWrongState(t *testing.T) {
	out := make(chan callbackResult, 1)
	h := callbackHandler("expected", out)

	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/callback?state=wrong&error=denied", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("wrong-state status = %d, want 400", bad.Code)
	}
	select {
	case got := <-out:
		t.Fatalf("wrong-state callback consumed result: %+v", got)
	default:
	}

	good := httptest.NewRecorder()
	h.ServeHTTP(good, httptest.NewRequest(http.MethodGet, "/callback?state=expected&code=ok", nil))
	if good.Code != http.StatusOK {
		t.Fatalf("good status = %d, want 200", good.Code)
	}
	got := <-out
	if got.err != nil || got.code != "ok" {
		t.Fatalf("callback result = %+v", got)
	}
}

func TestCallbackHandlerPublishesOnceConcurrently(t *testing.T) {
	out := make(chan callbackResult, 1)
	h := callbackHandler("expected", out)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/callback?state=expected&code=ok", nil))
		}()
	}
	wg.Wait()
	if got := len(out); got != 1 {
		t.Fatalf("published results = %d, want 1", got)
	}
}
