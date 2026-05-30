package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/happyarch/naviamp-sidecar/internal/auth"
)

// navidromeOK returns a test server that simulates a Navidrome ping responding
// with status "ok" — i.e., credentials are valid.
func navidromeOK(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"subsonic-response": map[string]any{"status": "ok"},
		})
	}))
}

// navidromeFail returns a test server that simulates a Navidrome ping
// rejecting the credentials with error code 40.
func navidromeFail(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"subsonic-response": map[string]any{
				"status": "failed",
				"error":  map[string]any{"code": 40, "message": "Wrong username or password."},
			},
		})
	}))
}

func TestVerify_OK(t *testing.T) {
	srv := navidromeOK(t)
	defer srv.Close()

	if err := auth.Verify(srv.URL, "alice", "token", "salt"); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestVerify_WrongCredentials(t *testing.T) {
	srv := navidromeFail(t)
	defer srv.Close()

	err := auth.Verify(srv.URL, "alice", "badtoken", "salt")
	if err == nil {
		t.Fatal("expected error for wrong credentials, got nil")
	}

	var ve *auth.VerifyError
	if !errorAs(err, &ve) {
		t.Fatalf("expected *auth.VerifyError, got %T: %v", err, err)
	}
	if ve.Code != 40 {
		t.Errorf("expected code 40, got %d", ve.Code)
	}
}

func TestVerify_NetworkError(t *testing.T) {
	// Point at a port nothing is listening on.
	err := auth.Verify("http://127.0.0.1:1", "alice", "token", "salt")
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

// errorAs is a local helper to avoid importing errors in the test file
// alongside the standard errors.As — both are fine but this keeps it readable.
func errorAs(err error, target any) bool {
	type asInterface interface {
		As(any) bool
	}
	// Use errors.As via reflection-free type assertion on the target pointer.
	// For this test we only use *auth.VerifyError, so a direct type switch suffices.
	switch t := target.(type) {
	case **auth.VerifyError:
		var ve *auth.VerifyError
		if x, ok := err.(*auth.VerifyError); ok {
			ve = x
			*t = ve
			return true
		}
		return false
	}
	return false
}
