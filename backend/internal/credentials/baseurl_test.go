package credentials

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appdb "github.com/github-radar/github-radar/internal/db"
)

func newBaseURLService(t *testing.T) (*Service, *int32) {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var hits int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Requires authentication"}`)
			return
		}
		hits++
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"id":583231,"login":"octocat"}`)
		case "/rate_limit":
			fmt.Fprint(w, `{"resources":{"core":{"limit":5000,"used":1,"remaining":4999,"reset":1792713600},`+
				`"search":{"limit":30,"used":1,"remaining":29,"reset":1792713600}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	}))
	t.Cleanup(stub.Close)
	service, err := NewService(store.SQL(), make([]byte, 32), WithGitHubBaseURL(stub.URL))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, &hits
}

// Credential verification must honour GITHUB_API_BASE_URL, otherwise an
// Enterprise or stubbed deployment cannot add credentials at all.
func TestCreateVerifiesAgainstConfiguredGitHubBaseURL(t *testing.T) {
	service, hits := newBaseURLService(t)
	credential, err := service.Create(context.Background(), CreateInput{Label: "primary", Token: "dummy-token-123456"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if credential.AuthLogin != "octocat" {
		t.Errorf("auth_login = %q", credential.AuthLogin)
	}
	if *hits < 2 {
		t.Errorf("stub hits = %d, want /user and /rate_limit", *hits)
	}
}
