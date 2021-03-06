package main

import (
	"crypto/sha1"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type ErrParseSession struct {
	session string
}

func (e ErrParseSession) Error() string {
	return "Invalid session string: " + e.session
}

type ErrNoRedirects struct{}

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

type Session struct {
	Url   string
	Name  string
	Value string
}

func NewSession(s string) (*Session, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return nil, ErrParseSession{session: s}
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if len(parts[i]) == 0 {
			return nil, ErrParseSession{session: s}
		}
	}
	//TODO: verify url
	//TODO: Add option to use user:password and generate session from that.
	cookie := strings.SplitN(parts[1], "=", 2)
	return &Session{Url: parts[0], Name: cookie[0], Value: cookie[1]}, nil
}

// Generate a SHA1 Hash of the dump (tree structure of node)
// which is used to compare to previous dumps.
// Must get a 200 for success with no redirects.
// curl version:
// curl -k --silent --fail { --user "<usr>:<pwd>" | --cookie "<sessionID>" } "$url/docroot/gato/dump.jsp?repository=$repo&depth=999&path=/$path" | sort | sha1sum
// We need to sort the dump, as the dump does NOT always return in the same order from request to request.
// TODO: dump jsp file generates a hash in the header with HEAD requests, instead of us generating one; that way only the hash needs to be sent over the network.
func (s *Session) hashDump(n *Node) (string, error) {
	req, err := http.NewRequest("GET", s.Url+"/docroot/gato/dump.jsp?repository="+n.Repo+"&depth=999&path=/"+n.Path, nil)
	if err != nil {
		return "", err
	}
	// Session cookie or basic authentication is not
	// required for a dump of website and dms repos,
	// but it is required for all other repos; so we
	// just include in all requests.
	req.AddCookie(&http.Cookie{Name: s.Name, Value: s.Value, Path: "/", Domain: "txstate.edu"})
	// Dumps can benefit from default acceptance of gzip content; As dumps are not that big
	// we will not run into Magnolia CMS gzip 2GB size constraint issue so will allow at this point.
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

	// Hate to read all at once instead of stream to hash, but
	// need the entire list to sort. Apparently dump.jsp can
	// return list in a different order upon each request.
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	lines := strings.Split(string(body), "\n")
	sort.Strings(lines)
	for _, l := range lines {
		h.Write([]byte(l))
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
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
	// body := bytes.NewBufferString("mgnlRepository=" + n.Repo + "&mgnlPath=/" + n.Path + "&ext=.xml&command=exportxml&exportxml=Export&mgnlKeepVersions=true")

	// gato is expecting form data so must POST with data in url encoded body.
	// req, err := http.NewRequest("POST", s.Url+"/.magnolia/pages/export.html", body)
	// /docroot/gato/export.jsp?repo=website&path=/testing-site-destroyer
	req, err := http.NewRequest("GET", s.Url+"/docroot/gato/export.jsp?repo="+n.Repo+"&path=/"+n.Path, nil)
	if err != nil {
		return err
	}
	req.AddCookie(&http.Cookie{Name: s.Name, Value: s.Value, Path: "/", Domain: "txstate.edu"})
	//req.Header.Add("content-type", `application/x-www-form-urlencoded`)
	// Magnolia CMS gzip responses have a 2GB limit; so do not accept gzip content to avoid this issue.
	tr := &http.Transport{
		DisableCompression: true,
	}
	// requires referrer header to pass gato csrfSecurity filters
	//req.Header.Add("referer", s.Url+"/.magnolia/pages/export.html")
	client := &http.Client{
		CheckRedirect: noRedirectPolicyFunc,
		Transport:     tr,
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
