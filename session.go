package main

import (
	"fmt"
	"strconv"
	"strings"
	"io"
	"bytes"
	"net/http"
	"crypto/sha1"
)

type ErrParseSession struct {
	session string
}
func (e ErrParseSession) Error() string {
	return "Invalid session string: " + e.session
}

type ErrNoRedirects struct {}

func (e ErrNoRedirects) Error() string {
	return "Encoundered a redirect"
}

func noRedirectPolicyFunc(_ *http.Request, _ []*http.Request) error {
	return ErrNoRedirects{}
}

type ErrNon200StatusCode struct {
	code int
}
func (e ErrNon200StatusCode) Error() string {
	return "Returned non 200 status code: " + strconv.Itoa(e.code)
}

type Session struct{
	Url string
	Name string
	Value string
}

func NewSession(s string) (*Session, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return nil, ErrParseSession{ session: s }
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if len(parts[i]) == 0 {
			return nil, ErrParseSession{ session: s }
		}
	}
	//TODO: verify url
	//TODO: Add option to use user:password and generate session from that.
	cookie := strings.SplitN(parts[1], "=", 2)
	return &Session{ Url: parts[0], Name: cookie[0], Value: cookie[1] }, nil
}

// Generate a SHA1 Hash of the dump (tree structure of node)
// which is used to compare to previous dumps.
// Must get a 200 for success with no redirects.
// curl version:
// curl -k --silent --fail { --user "<usr>:<pwd>" | --cookie "<sessionID>" } "$url/docroot/gato/dump.jsp?repository=$repo&depth=999&path=/$path" | sort | sha1sum
// QUESTION: Do we really need to sort the dump? Shouldn't the dump always return in the same order from request to request? For now we are not sorting; Also
// we should have the dump be able to also generate a hash instead of us generating one; that way only the hash needs to be sent over the network.
func (s *Session) hashDump(n *Node) (string, error) {
	req, err := http.NewRequest("GET", s.Url + "/docroot/gato/dump.jsp?repository="+n.Repo+"&depth=999&path=/"+n.Path, nil)
	if err != nil {
		return "", err
	}
	// Session cookie or basic authentication is not
	// required for a dump of website and dms repos,
	// but it is required for all other repos; so we
	// just include in all requests.
	req.AddCookie(&http.Cookie{Name: s.Name, Value: s.Value, Path: "/", Domain: "txstate.edu"})
	// referrer not not required for dump as gato csrfSecurity filters only apply to /.magnolia section
	client := &http.Client{
		CheckRedirect: noRedirectPolicyFunc,
	}
	res, err := client.Do(req)
	// Always have to close body if it exists,
	// whether we use it or not.
	if res != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return "", err
	}

	// Check for 200 status
	if res.StatusCode != 200 {
		return "", ErrNon200StatusCode{code: res.StatusCode}
	}

	h := sha1.New()
	if _, err = io.Copy(h, res.Body); err != nil {
		return "", err
	}
	hash := fmt.Sprintf("%x", h.Sum(nil))
	return hash, nil
}

// Must get a 200 for success with no redirects
// curl version:
// curl --silent --fail { --user "<usr>:<pwd>" | --cookie "<sessionID>" } --referer "<url>/.magnolia/pages/export.html" --data "mgnlRepository=$repo&mgnlPath=/$path&ext=.xml&command=exportxml&exportxml=Export&mgnlKeepVersions=true" "<url>/.magnolia/pages/export.html" > "$rdir/$repo/$repo.$node.xml.<date>"
func (s *Session) saveNode(n *Node, w io.Writer) error {
	// NOTE: Why mgnlKeepVersions is set to true:
	// This flag is really to being used to keep magnolia
	// from filtering out versions as that can lead to a
	// disconnect bug found in Java Xerces XML parser.
	// Also the versions are actually kept in the root
	// jcr node; so even with this flag set to true, no
	// versions will be coming over. A side benefit of
	// this is that we immediately start to get data back.
	body := bytes.NewBufferString("mgnlRepository=" + n.Repo + "&mgnlPath=/" + n.Path + "&ext=.xml&command=exportxml&exportxml=Export&mgnlKeepVersions=true")

	// gato is expecting form data so must POST with data in url encoded body.
	req, err := http.NewRequest("POST", s.Url + "/.magnolia/pages/export.html", body)
	if err != nil {
		return err
	}
	req.AddCookie(&http.Cookie{Name: s.Name, Value: s.Value, Path: "/", Domain: "txstate.edu"})
	req.Header.Add("content-type", `application/x-www-form-urlencoded`)
	// requires referrer header to pass gato csrfSecurity filters
	req.Header.Add("referer", s.Url + "/.magnolia/pages/export.html")
	client := &http.Client{
		CheckRedirect: noRedirectPolicyFunc,
	}
	res, err := client.Do(req)
	// Always have to close body if it exists,
	// whether we use it or not.
	if res != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return err
	}

	// Check for 200 status
	if res.StatusCode != 200 {
		return ErrNon200StatusCode{code: res.StatusCode}
	}

	// Write body out; (ex using flush: net.http.httputil.ReverseProxy)
	_, err = io.Copy(w, res.Body)
	return err
}
