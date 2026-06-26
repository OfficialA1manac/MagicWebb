package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearch_BadInput(t *testing.T) {
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
			app := fiber.New()
			svc := NewSearchService(nil)
			app.Get("/api/v1/search", svc.handleSearch)
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			resp, err := app.Test(req)
			require.NoError(t, err)
			assert.Equal(t, tc.status, resp.StatusCode)
			var body map[string]string
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			assert.Equal(t, tc.errMsg, body["error"])
		})
	}
}
