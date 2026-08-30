package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ── Cross-network profile carry-over (fanout) ───────────────────────────────
//
// Profiles are edited per network, so a wallet that filled one in on Coston2
// still gets a face on Songbird: when the LOCAL profile is empty the service
// asks its siblings. These tests cover the parts that are easy to break and
// invisible when broken — the anti-ping-pong header, which sibling's answer
// wins, and that a sick sibling never breaks the local response.

const fanoutAddr = "0x00000000000000000000000000000000000000aa"

// emptyProfileRows is what the DB returns for an address with no local profile.
func emptyProfileRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"display_name", "tag", "bio", "avatar_uri", "banner_uri", "twitter", "website",
	}).AddRow("", "", "", "", "", "", "")
}

func expectEmptyLocalProfile(mock pgxmock.PgxPoolIface, addr string) {
	mock.ExpectQuery(`SELECT display_name, COALESCE\(tag,''\), bio`).
		WithArgs(addr).
		WillReturnRows(emptyProfileRows())
}

// newFanoutService builds a ProfilesService whose siblings are the given test
// server URLs, in order.
func newFanoutService(t *testing.T, mock pgxmock.PgxPoolIface, siblingURLs ...string) *ProfilesService {
	t.Helper()
	cfg := &config.Config{ChainID: 19} // this process serves Songbird
	cfg.Networks = []config.Network{{ChainID: 19, Name: "Songbird", Current: true}}
	for i, u := range siblingURLs {
		cfg.Networks = append(cfg.Networks, config.Network{
			// 114 sorts first in NewProfilesService; give the later siblings
			// other ids so the declared order is preserved after that.
			ChainID:   uint64(114 + i*100),
			Name:      "sibling",
			URL:       u,
			Available: true,
		})
	}
	return NewProfilesService(db.New(mock), cfg)
}

// An empty local profile is answered from a sibling, stamped with the
// SIBLING's chain id — the caller must be able to tell where the profile came
// from — and the outbound request must carry the fanout header.
func TestProfilesFanout_CarriesOverFromSibling(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	expectEmptyLocalProfile(mock, fanoutAddr)

	var sawHeader atomic.Bool
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(profileFanoutHeader) != "" {
			sawHeader.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"display_name":"Alice","tag":"OG Minter","bio":"hi"}`))
	}))
	defer sibling.Close()

	svc := newFanoutService(t, mock, sibling.URL)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/profile/:addr", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/profile/"+fanoutAddr)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		DisplayName string `json:"display_name"`
		Tag         string `json:"tag"`
		SourceChain uint64 `json:"source_chain"`
	}
	decodeJSON(t, resp, &got)

	if got.DisplayName != "Alice" || got.Tag != "OG Minter" {
		t.Fatalf("body = %+v, want the sibling's profile", got)
	}
	// 19 would mean "this is a local profile", which would be a lie.
	if got.SourceChain != 114 {
		t.Fatalf("source_chain = %d, want the sibling's chain id 114", got.SourceChain)
	}
	if !sawHeader.Load() {
		t.Fatalf("sibling request lacked %s — without it two networks read-through to each other forever", profileFanoutHeader)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A request that IS a sibling's read-through must not fan out again. This is
// the loop guard: without it, two networks that both lack the profile call
// each other until something times out.
func TestProfilesFanout_FanoutRequestDoesNotRecurse(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	expectEmptyLocalProfile(mock, fanoutAddr)

	var calls atomic.Int64
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"display_name":"Alice"}`))
	}))
	defer sibling.Close()

	svc := newFanoutService(t, mock, sibling.URL)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/profile/:addr", svc.handleGet)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/profile/"+fanoutAddr, nil)
	req.Header.Set(profileFanoutHeader, "1")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("sibling was called %d times on a fanout request, want 0", n)
	}

	var got struct {
		DisplayName string `json:"display_name"`
		SourceChain uint64 `json:"source_chain"`
	}
	decodeJSON(t, resp, &got)
	if got.DisplayName != "" {
		t.Fatalf("display_name = %q, want empty — a fanout request answers locally only", got.DisplayName)
	}
	if got.SourceChain != 19 {
		t.Fatalf("source_chain = %d, want 19 (local)", got.SourceChain)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Every sibling failure mode degrades to the local (empty) profile rather than
// failing the request: a sibling being down must never take this network's
// profile endpoint with it.
func TestProfilesFanout_SickSiblingsDegradeToLocal(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	expectEmptyLocalProfile(mock, fanoutAddr)

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer garbage.Close()
	// Empty-but-valid: has no profile either, so it must be skipped rather
	// than served as an answer.
	blank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"display_name":"","tag":"","bio":"","avatar_uri":""}`))
	}))
	defer blank.Close()

	svc := newFanoutService(t, mock, down.URL, garbage.URL, blank.URL)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/profile/:addr", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/profile/"+fanoutAddr)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a sick sibling must not break the local endpoint", resp.StatusCode)
	}
	var got struct {
		DisplayName string `json:"display_name"`
		SourceChain uint64 `json:"source_chain"`
	}
	decodeJSON(t, resp, &got)
	if got.DisplayName != "" {
		t.Fatalf("display_name = %q, want empty", got.DisplayName)
	}
	if got.SourceChain != 19 {
		t.Fatalf("source_chain = %d, want 19 (fell back to local)", got.SourceChain)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
