package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSearch_BadInput(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		status int
		errMsg string
	}{
		{"missing_q", "/api/v1/search", http.StatusBadRequest, "q must be at least 2 characters"},
		{"empty_q", "/api/v1/search?q=", http.StatusBadRequest, "q must be at least 2 characters"},
		{"single_char", "/api/v1/search?q=a", http.StatusBadRequest, "q must be at least 2 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			rec := httptest.NewRecorder()
			// nil db.Q is safe here — bad-input path returns before any DB call
			handleSearch(nil)(rec, req)
			assert.Equal(t, tc.status, rec.Code)
			var body map[string]string
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
			assert.Equal(t, tc.errMsg, body["error"])
		})
	}
}

func TestHandleSearch_ContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=a", nil)
	rec := httptest.NewRecorder()
	handleSearch(nil)(rec, req)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}
