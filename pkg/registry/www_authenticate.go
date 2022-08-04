package registry

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	auth "github.com/abbot/go-http-auth"
)

func parseWWWAuthenticateHeader(value string) (string, map[string]string, error) {
	s := strings.SplitN(value, " ", 2)
	if len(s) < 2 {
		return "", nil, errors.New("malformed www-authenticate header")
	}
	return s[0], auth.ParsePairs(s[1]), nil
}

func NewWWWAuthenticateRequest(wwwAuthenticate string) (string, *http.Request, error) {
	kind, values, err := parseWWWAuthenticateHeader(wwwAuthenticate)
	if err != nil {
		return "", nil, err
	}
	u, err := url.Parse(values["realm"])
	if err != nil {
		return "", nil, err
	}
	q := u.Query()
	for k, v := range values {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return "", nil, err
	}
	return kind, req, nil

}
