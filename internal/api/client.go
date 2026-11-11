// Package api is the steermi CLI's client for the Steermi gateway.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/steermi/cli/internal/buildinfo"
	"github.com/steermi/cli/internal/config"
)

// Error is a gateway error envelope.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Code, e.Status)
}

// IsCode reports whether err is an API error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Client calls the gateway at <url>/v1.
type Client struct {
	URL   string
	HTTP  *http.Client
	creds *config.Credentials
	// Now and Save are seams for tests.
	Now  func() time.Time
	Save func(*config.Credentials) error
}

// New creates a client for a base URL without credentials (login).
func New(url string) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), HTTP: &http.Client{Timeout: 60 * time.Second}, Now: time.Now, Save: config.SaveCredentials}
}

// FromCredentials creates a client for the paired device.
func FromCredentials(c *config.Credentials) *Client {
	cl := New(c.URL)
	cl.creds = c
	return cl
}

// Load creates a client from stored credentials.
func Load() (*Client, error) {
	c, err := config.LoadCredentials()
	if err != nil {
		return nil, err
	}
	return FromCredentials(c), nil
}

// Credentials returns the client's device credentials (nil before login).
func (c *Client) Credentials() *config.Credentials { return c.creds }

// Auth selects the credential for a call.
type Auth struct {
	kind  int
	token string
}

var (
	// NoAuth sends no credential (pairing exchange, refresh).
	NoAuth = Auth{kind: 0}
	// DeviceAuth sends the device access token, refreshing it when needed.
	DeviceAuth = Auth{kind: 1}
)

// TokenAuth sends an explicit bearer token (a launch token).
func TokenAuth(token string) Auth { return Auth{kind: 2, token: token} }

// DeviceToken returns a valid device access token, refreshing when it
// expires within a minute.
func (c *Client) DeviceToken(ctx context.Context) (string, error) {
	if c.creds == nil {
		return "", config.ErrNotLoggedIn
	}
	if c.creds.AccessToken != "" && c.Now().Add(time.Minute).Before(c.creds.AccessExpiry) {
		return c.creds.AccessToken, nil
	}
	return c.refresh(ctx)
}

func (c *Client) refresh(ctx context.Context) (string, error) {
	var resp DeviceToken
	if err := c.Do(ctx, http.MethodPost, "/ai/devices/token", map[string]string{"refresh_token": c.creds.RefreshToken}, &resp, NoAuth); err != nil {
		if IsCode(err, "DEVICE_REVOKED") || IsCode(err, "DEVICE_MEMBERSHIP_INACTIVE") {
			return "", fmt.Errorf("%w — run `steermi login` again", err)
		}
		return "", err
	}
	c.creds.AccessToken = resp.AccessToken
	if exp, err := time.Parse(time.RFC3339, resp.ExpiresAt); err == nil {
		c.creds.AccessExpiry = exp
	} else {
		c.creds.AccessExpiry = c.Now().Add(10 * time.Minute)
	}
	if c.Save != nil {
		if err := c.Save(c.creds); err != nil {
			return "", fmt.Errorf("save credentials: %w", err)
		}
	}
	return resp.AccessToken, nil
}

// Do performs a call and decodes the envelope's data into out. A device call
// that fails with TOKEN_EXPIRED is retried once after a refresh.
func (c *Client) Do(ctx context.Context, method, path string, body, out any, auth Auth) error {
	err := c.do(ctx, method, path, body, out, auth)
	if auth.kind == 1 && (IsCode(err, "TOKEN_EXPIRED") || IsCode(err, "TOKEN_INVALID")) && c.creds != nil {
		c.creds.AccessToken = ""
		if _, rerr := c.refresh(ctx); rerr != nil {
			return rerr
		}
		return c.do(ctx, method, path, body, out, auth)
	}
	return err
}

func (c *Client) do(ctx context.Context, method, path string, body, out any, auth Auth) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+"/v1"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	switch auth.kind {
	case 1:
		token, err := c.DeviceToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	case 2:
		req.Header.Set("Authorization", "Bearer "+auth.token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Steermi at %s: %w", c.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode >= 300 {
		e := &Error{Status: resp.StatusCode, Code: "HTTP_" + fmt.Sprint(resp.StatusCode)}
		if env.Error != nil {
			e.Code, e.Message = env.Error.Code, env.Error.Message
		}
		return e
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}
