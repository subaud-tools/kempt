package release

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/subaud-tools/tools-common/auth"
)

func TestRealReleasesLatestTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/schuettc/x/releases/latest" {
			http.Redirect(w, r, "/schuettc/x/releases/tag/v1.2.3", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	rr := RealReleases{Client: srv.Client(), Base: srv.URL}
	tag, err := rr.LatestTag("schuettc/x")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.2.3" {
		t.Fatalf("tag = %q, want v1.2.3", tag)
	}
}

func TestRealReleasesLatestTagMalformedRedirect(t *testing.T) {
	// Location header present but has no /tag/ segment.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/schuettc/x/releases/latest" {
			http.Redirect(w, r, "/schuettc/x/releases/expanded_assets/v1.2.3", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	rr := RealReleases{Client: srv.Client(), Base: srv.URL}
	_, err := rr.LatestTag("schuettc/x")
	if err == nil {
		t.Fatal("expected error for redirect without /tag/ segment")
	}
	if !strings.Contains(err.Error(), "/tag/") {
		t.Fatalf("error %q should mention missing /tag/ segment", err.Error())
	}
}

func TestRealReleasesLatestTagNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rr := RealReleases{Client: srv.Client(), Base: srv.URL}
	if _, err := rr.LatestTag("schuettc/x"); err == nil {
		t.Fatal("expected error on non-redirect response")
	}
}

func TestRealReleasesDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			_, _ = w.Write([]byte("hello"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	rr := RealReleases{Client: srv.Client(), Base: srv.URL}

	body, err := rr.Download(srv.URL + "/ok")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}

	if _, err := rr.Download(srv.URL + "/missing"); err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestFakeReleasesLatestTagMissingRepo(t *testing.T) {
	f := FakeReleases{Tags: map[string]string{}}
	_, err := f.LatestTag("example/missing")
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
	if !strings.Contains(err.Error(), "example/missing") {
		t.Fatalf("error %q should name the repo", err.Error())
	}
}

func TestFakeReleasesDownloadMissing(t *testing.T) {
	f := FakeReleases{Files: map[string][]byte{}}
	_, err := f.Download("https://example.com/x")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "https://example.com/x") {
		t.Fatalf("error %q should name the url", got)
	}
}

func TestRealReleasesLinkAsksTheAccessService(t *testing.T) {
	var got struct{ Product, Path, Auth string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/links" {
			http.NotFound(w, r)
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		got.Product, got.Path, got.Auth = in["product"], in["path"], r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"url":"https://hail.tools/dl/hail/latest?Signature=x"}`))
	}))
	defer srv.Close()
	t.Setenv(auth.TokenEnv, "tok")

	rr := RealReleases{Auth: &auth.Client{API: srv.URL, HTTP: srv.Client()}}
	u, err := rr.Link("hail", "hail/latest")
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://hail.tools/dl/hail/latest?Signature=x" || got.Product != "hail" || got.Path != "hail/latest" || got.Auth != "Bearer tok" {
		t.Fatalf("link %q, request %+v", u, got)
	}
}

func TestRealReleasesDownloadErrorHidesSignature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	rr := RealReleases{Client: srv.Client()}
	_, err := rr.Download(srv.URL + "/dl/hail/latest?Signature=secret")
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "/dl/hail/latest") {
		t.Fatalf("err = %v, want the file named without its query", err)
	}
}
