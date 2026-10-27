package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/subaud-tools/tools-common/auth"
)

// Releases is the interface for querying and downloading releases.
// Wired in Task 7; declared here now to avoid an import cycle since
// machine.Context declares a Releases field.
type Releases interface {
	LatestTag(repo string) (string, error)
	Download(url string) ([]byte, error)
	// Link returns a signed URL of path in product's downloads, for a gated
	// site whose /dl is closed to anyone.
	Link(product, path string) (string, error)
	// SignIn runs one short step of this machine's subaud.tools sign-in:
	// it starts one (printing where to type the code) or finishes one
	// already started, writing to out.
	SignIn(out io.Writer) error
}

const defaultBase = "https://github.com"

// RealReleases resolves and downloads GitHub release assets over HTTP.
// The zero value is usable: Client defaults to http.DefaultClient and Base
// defaults to https://github.com. Tests point Base at an httptest server.
type RealReleases struct {
	Client *http.Client
	Base   string       // defaults to https://github.com
	Auth   *auth.Client // asks for signed links; nil → the access service
}

func (r RealReleases) base() string {
	if r.Base != "" {
		return strings.TrimRight(r.Base, "/")
	}
	return defaultBase
}

// LatestTag issues a GET to <Base>/<repo>/releases/latest and captures the
// redirect Location without following it. The tag is the path segment after
// "/tag/". A non-redirect response or a Location lacking that segment is an
// error.
func (r RealReleases) LatestTag(repo string) (string, error) {
	// Clone the client so we can install a capturing CheckRedirect without
	// mutating the caller's client.
	base := http.DefaultClient
	if r.Client != nil {
		base = r.Client
	}
	c := &http.Client{
		Transport: base.Transport,
		Jar:       base.Jar,
		Timeout:   base.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	url := fmt.Sprintf("%s/%s/releases/latest", r.base(), repo)
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("latest tag for %s: no redirect from %s (status %d)", repo, url, resp.StatusCode)
	}
	idx := strings.Index(loc, "/tag/")
	if idx < 0 {
		return "", fmt.Errorf("latest tag for %s: redirect %q has no /tag/ segment", repo, loc)
	}
	tag := loc[idx+len("/tag/"):]
	if i := strings.IndexByte(tag, '/'); i >= 0 {
		tag = tag[:i]
	}
	if tag == "" {
		return "", fmt.Errorf("latest tag for %s: empty tag in redirect %q", repo, loc)
	}
	return tag, nil
}

// SignIn runs one step of this machine's sign-in, named by its hostname.
func (r RealReleases) SignIn(out io.Writer) error {
	c := r.Auth
	if c == nil {
		c = &auth.Client{}
	}
	name, _ := os.Hostname()
	return c.Login(context.Background(), name, runtime.GOOS, out)
}

// Link asks the access service for a five-minute signed URL of path in
// product's downloads, with this machine's token.
func (r RealReleases) Link(product, path string) (string, error) {
	c := r.Auth
	if c == nil {
		c = &auth.Client{}
	}
	return c.Link(context.Background(), product, path)
}

// Download issues a GET and returns the body. A non-200 response is an error.
// Errors name the URL without its query: a signed link's query is its
// signature.
func (r RealReleases) Download(url string) ([]byte, error) {
	c := http.DefaultClient
	if r.Client != nil {
		c = r.Client
	}
	file, signed := url, false
	if i := strings.IndexByte(url, '?'); i >= 0 {
		file, signed = url[:i], true
	}
	resp, err := c.Get(url)
	if err != nil {
		if !signed {
			return nil, err
		}
		var ue *neturl.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("download %s: %w", file, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: status %d", file, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// GatedSites are the download sites whose /dl is closed to anyone: every
// file there is fetched through a signed link.
var GatedSites = []string{"hail.tools", "diazo.tools"}

// Fetch downloads path (e.g. "hail/latest" or
// "hail/0.4.0/hail_darwin_arm64.tar.gz") under site's /dl: through a signed
// link of product when the site is gated, else plainly.
func Fetch(r Releases, site, product, path string) ([]byte, error) {
	if !slices.Contains(GatedSites, site) {
		return r.Download("https://" + site + "/dl/" + path)
	}
	link, err := r.Link(product, path)
	if errors.Is(err, auth.ErrNotSignedIn) || errors.Is(err, auth.ErrSignInRefused) {
		// Sign in in short steps: this run starts it (or finishes one
		// started earlier); once the machine is allowed, `kempt login`
		// finishes it and the command carries on.
		var out strings.Builder
		if err := r.SignIn(&out); err != nil {
			return nil, errors.New(applyAgain(err.Error()))
		}
		if link, err = r.Link(product, path); err != nil {
			return nil, errors.New(applyAgain("this machine is not signed in to subaud.tools yet.\n" + out.String()))
		}
	} else if err != nil {
		return nil, err
	}
	return r.Download(link)
}

// applyAgain points tools-common's sign-in lines at the command that will
// finish signing in.
func applyAgain(s string) string {
	// Not `kempt apply`: with the tool already installed it downloads nothing,
	// so it never reaches the sign-in. `kempt login` always does.
	s = strings.ReplaceAll(s, "run login again once you've allowed this machine.", "run `kempt login` once you've allowed this machine, then this command again.")
	return strings.TrimSpace(strings.ReplaceAll(s, "run login again", "run `kempt login` again"))
}

// FakeReleases is an in-memory Releases for tests. Tags maps repo → tag,
// Files maps url → body and Links maps "<product> <path>" → signed URL;
// LinkErr, when set, is what every Link returns.
type FakeReleases struct {
	Tags    map[string]string
	Files   map[string][]byte
	Links   map[string]string
	LinkErr error
	// SignInText is what SignIn prints; the sign-in never completes.
	SignInText string
}

func (f FakeReleases) SignIn(out io.Writer) error {
	_, err := io.WriteString(out, f.SignInText)
	return err
}

func (f FakeReleases) LatestTag(repo string) (string, error) {
	tag, ok := f.Tags[repo]
	if !ok {
		return "", fmt.Errorf("no tag for repo %s", repo)
	}
	return tag, nil
}

func (f FakeReleases) Download(url string) ([]byte, error) {
	body, ok := f.Files[url]
	if !ok {
		return nil, fmt.Errorf("no file for url %s", url)
	}
	return body, nil
}

func (f FakeReleases) Link(product, path string) (string, error) {
	if f.LinkErr != nil {
		return "", f.LinkErr
	}
	u, ok := f.Links[product+" "+path]
	if !ok {
		return "", fmt.Errorf("no link for %s %s", product, path)
	}
	return u, nil
}
