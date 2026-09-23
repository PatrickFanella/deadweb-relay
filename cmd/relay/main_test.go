package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"deadweb-relay/internal/ratelimit"
	"deadweb-relay/internal/store"
)

func testApp(t *testing.T) *app {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &app{
		store:        s,
		siteName:     "Test Relay",
		baseURL:      "https://relay.example.test",
		adminToken:   "test-admin-token",
		hmacKey:      []byte("test-hmac-key"),
		maxWrite:     1024,
		writeLimiter: ratelimit.New(60, time.Hour),
	}
}

func TestHealthz(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	a.middleware(http.HandlerFunc(a.route)).ServeHTTP(w, r)

	if w.Code != http.StatusOK || w.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestMiddlewareHeadersAndAdminAuthentication(t *testing.T) {
	a := testApp(t)
	h := a.middleware(http.HandlerFunc(a.route))

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/__events.ndjson", nil))
	if unauthorized.Code != http.StatusNotFound {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	if got := unauthorized.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Fatalf("CSP = %q", got)
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/__events.ndjson", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer test-admin-token")
	authorized := httptest.NewRecorder()
	h.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d", authorized.Code)
	}
}

func TestNoteWriteRejectsCredentialLikeContent(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest(http.MethodPut, "/notes/example", strings.NewReader("api_key=abcdefghijklmnopqrstu"))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	a.middleware(http.HandlerFunc(a.route)).ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
}

func TestDiscoveryAdvertisesConfiguredURL(t *testing.T) {
	a := testApp(t)
	w := httptest.NewRecorder()
	a.middleware(http.HandlerFunc(a.route)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Sitemap: https://relay.example.test/sitemap.xml") {
		t.Fatalf("robots response = %d %q", w.Code, w.Body.String())
	}
}

func TestGuestbookPaginationReturnsNewestEntries(t *testing.T) {
	a := testApp(t)
	for i := 0; i < 51; i++ {
		if _, err := a.store.AddGuestbook("writer", "entry", 0); err != nil {
			t.Fatal(err)
		}
	}

	first := httptest.NewRecorder()
	a.middleware(http.HandlerFunc(a.route)).ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/guestbook/?format=json&page=1", nil))
	if first.Code != http.StatusOK || strings.Count(first.Body.String(), `"id"`) != 50 {
		t.Fatalf("first page = %d with %d entries", first.Code, strings.Count(first.Body.String(), `"id"`))
	}

	second := httptest.NewRecorder()
	a.middleware(http.HandlerFunc(a.route)).ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/guestbook/?format=json&page=2", nil))
	if second.Code != http.StatusOK || strings.Count(second.Body.String(), `"id"`) != 1 {
		t.Fatalf("second page = %d with %d entries", second.Code, strings.Count(second.Body.String(), `"id"`))
	}
}

func TestAuthoredArtifactPathsExist(t *testing.T) {
	a := testApp(t)
	h := a.middleware(http.HandlerFunc(a.route))
	for path, expected := range map[string]string{
		"/archive/old-server-notes.txt":   "permanent address",
		"/archive/2004-ideas.txt":         "hit counter graphs",
		"/tmp/test.txt":                   "still isn't temporary",
		"/tmp/hello.txt":                  "hello whoever finds this",
		"/README.TXT":                     "Stable links are intentional.",
		"/reference/plain-text.txt":       "not a visitor message",
		"/reference/text-formats.txt":     "invented stationery counts",
		"/reference/backup-checklist.txt": "successful restore is stronger",
		"/reference/changes.txt":          "reference-shelf-v1",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), expected) {
			t.Errorf("%s = %d %q", path, w.Code, w.Body.String())
		}
	}
}

func TestReferenceResourcesAreReadOnlyAndDiscoverable(t *testing.T) {
	a := testApp(t)
	h := a.middleware(http.HandlerFunc(a.route))
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/reference/plain-text.txt", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s returned %d", method, w.Code)
		}
	}
	for _, path := range []string{"/sitemap.xml", "/index.txt"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if !strings.Contains(w.Body.String(), "/reference/") {
			t.Fatalf("reference shelf absent from %s", path)
		}
	}
}
