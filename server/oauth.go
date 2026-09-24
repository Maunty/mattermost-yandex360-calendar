package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Maunty/mattermost-ya-calendar/server/internal/caldav"
)

// Where Yandex lives. These are fields on the client rather than constants in
// the calls so that tests can point the whole flow at a stand-in.
const (
	defaultAuthorizeURL = "https://oauth.yandex.ru/authorize"
	defaultTokenURL     = "https://oauth.yandex.ru/token"
	defaultUserInfoURL  = "https://login.yandex.ru/info?format=json"
	defaultCalDAVURL    = "https://caldav.yandex.ru/"
)

// yandexScope is the consent this plugin asks for.
//
// It is write-capable, and the plugin never writes. Probing isolated it as the
// only scope that opens CalDAV at all: the fine-grained read scopes
// (calendar:events.read, calendar:calendars.read) and the broad read-only
// scope (calendar:read_all) are each rejected with 401 using a token that is
// otherwise valid. Narrowing this is not a matter of changing the string; it
// would need Yandex to accept a narrower scope over CalDAV.
const yandexScope = "calendar:all"

// oauthStateTTL is how long a consent flow may take. Long enough to sign in
// and read a consent screen; short enough that an abandoned flow leaves
// nothing usable behind.
const oauthStateTTL = 15 * time.Minute

// refreshSkew refreshes a token slightly before it expires, so that a request
// is not sent with a credential that dies in flight.
const refreshSkew = 5 * time.Minute

type oauthClient struct {
	httpClient   *http.Client
	authorizeURL string
	tokenURL     string
	userInfoURL  string
	clientID     string
	clientSecret string
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// yandexAccount is what the provider says about the account that just
// consented. Identity comes from here rather than from matching email
// addresses, and it needs no scope of its own.
type yandexAccount struct {
	ID    string `json:"id"`
	Login string `json:"login"`
}

// oauthError is a refusal from the OAuth endpoints, carrying enough to tell a
// dead grant from a provider having a bad afternoon.
type oauthError struct {
	StatusCode  int
	Code        string
	Description string
}

func (e *oauthError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("yandex oauth: %d %s: %s", e.StatusCode, e.Code, e.Description)
	}
	return fmt.Sprintf("yandex oauth: %d", e.StatusCode)
}

// isAuthFailure reports a grant that will never work again without fresh
// consent, as opposed to a request that is worth trying later.
func (e *oauthError) isAuthFailure() bool {
	switch e.Code {
	case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_scope", "access_denied":
		return true
	}
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

// AuthorizeURL is where the person goes to approve access.
func (c *oauthClient) AuthorizeURL(state, redirectURI string) string {
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", c.clientID)
	query.Set("scope", yandexScope)
	query.Set("state", state)
	query.Set("redirect_uri", redirectURI)
	// force_confirm lets somebody with several Yandex accounts pick which one
	// they are connecting, rather than being silently signed in as whichever
	// they last used.
	query.Set("force_confirm", "yes")
	return c.authorizeURL + "?" + query.Encode()
}

// Exchange turns the authorization code from the callback into tokens.
func (c *oauthClient) Exchange(ctx context.Context, code, redirectURI string) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	return c.postToken(ctx, form)
}

// Refresh renews an expired access token without involving the person.
func (c *oauthClient) Refresh(ctx context.Context, refreshToken string) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return c.postToken(ctx, form)
}

func (c *oauthClient) postToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &caldav.TransportError{Method: http.MethodPost, Href: c.tokenURL, Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &caldav.TransportError{Method: http.MethodPost, Href: c.tokenURL, Err: err}
	}

	if resp.StatusCode != http.StatusOK {
		failure := &oauthError{StatusCode: resp.StatusCode}
		var payload struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(body, &payload) == nil {
			failure.Code, failure.Description = payload.Error, payload.ErrorDescription
		}
		return nil, failure
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("yandex oauth: unreadable token response: %w", err)
	}
	if token.AccessToken == "" {
		return nil, errors.New("yandex oauth: the token response carried no access token")
	}
	return &token, nil
}

// Account asks Yandex who just connected. The login is what the person is
// told, so that somebody with several accounts can see they picked the wrong
// one. This endpoint answers without a login scope of its own.
func (c *oauthClient) Account(ctx context.Context, accessToken string) (*yandexAccount, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "OAuth "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &caldav.TransportError{Method: http.MethodGet, Href: c.userInfoURL, Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &caldav.TransportError{Method: http.MethodGet, Href: c.userInfoURL, Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &oauthError{StatusCode: resp.StatusCode}
	}

	var account yandexAccount
	if err := json.Unmarshal(body, &account); err != nil {
		return nil, fmt.Errorf("yandex oauth: unreadable account response: %w", err)
	}
	return &account, nil
}

// isAuthFailure reports a failure that means the Connection is dead, from
// wherever in the stack it came.
func isAuthFailure(err error) bool {
	if caldav.IsAuthFailure(err) {
		return true
	}
	var failure *oauthError
	if errors.As(err, &failure) {
		return failure.isAuthFailure()
	}
	return false
}

// isTransient reports a failure worth trying again. Conflating this with the
// one above would disconnect every user during a provider outage.
func isTransient(err error) bool {
	if caldav.IsTransient(err) {
		return true
	}
	var failure *oauthError
	if errors.As(err, &failure) {
		return !failure.isAuthFailure() && failure.StatusCode >= 500
	}
	return false
}
