package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/cli/internal/config"
)

func envelope(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func apiError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": "m"}})
}

func TestDeviceTokenRefreshAndRetry(t *testing.T) {
	var refreshes, calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/ai/devices/token":
			refreshes.Add(1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			assert.Equal(t, "std_refresh", body["refresh_token"])
			assert.Empty(t, r.Header.Get("Authorization"))
			envelope(w, 200, map[string]string{"access_token": "fresh", "expires_at": time.Now().Add(15 * time.Minute).Format(time.RFC3339)})
		case "/v1/ai/devices/me":
			if calls.Add(1) == 1 {
				apiError(w, 401, "TOKEN_EXPIRED")
				return
			}
			assert.Equal(t, "Bearer fresh", r.Header.Get("Authorization"))
			envelope(w, 200, map[string]any{"device": map[string]string{"id": "d1", "name": "mbp"}})
		}
	}))
	defer srv.Close()

	var saved *config.Credentials
	c := FromCredentials(&config.Credentials{URL: srv.URL, RefreshToken: "std_refresh", AccessToken: "stale", AccessExpiry: time.Now().Add(time.Hour)})
	c.Save = func(cr *config.Credentials) error { saved = cr; return nil }

	dev, err := c.Me(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mbp", dev.Name)
	assert.Equal(t, int32(1), refreshes.Load(), "one refresh after TOKEN_EXPIRED")
	require.NotNil(t, saved)
	assert.Equal(t, "fresh", saved.AccessToken)
}

func TestDeviceTokenRefreshesBeforeExpiry(t *testing.T) {
	var refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ai/devices/token" {
			refreshes.Add(1)
			envelope(w, 200, map[string]string{"access_token": "fresh", "expires_at": time.Now().Add(15 * time.Minute).Format(time.RFC3339)})
			return
		}
		envelope(w, 200, map[string]any{})
	}))
	defer srv.Close()
	c := FromCredentials(&config.Credentials{URL: srv.URL, RefreshToken: "r", AccessToken: "old", AccessExpiry: time.Now().Add(30 * time.Second)})
	c.Save = func(*config.Credentials) error { return nil }
	tok, err := c.DeviceToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "fresh", tok)
	tok, err = c.DeviceToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "fresh", tok)
	assert.Equal(t, int32(1), refreshes.Load(), "a valid token is reused")
}

func TestRevokedDeviceMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiError(w, 401, "DEVICE_REVOKED")
	}))
	defer srv.Close()
	c := FromCredentials(&config.Credentials{URL: srv.URL, RefreshToken: "r"})
	_, err := c.Me(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "steermi login")
	assert.True(t, IsCode(err, "DEVICE_REVOKED"))
}

func TestErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiError(w, 410, "LOCAL_LAUNCH_EXPIRED")
	}))
	defer srv.Close()
	c := New(srv.URL)
	err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil, TokenAuth("t"))
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 410, apiErr.Status)
	assert.Equal(t, "LOCAL_LAUNCH_EXPIRED", apiErr.Code)
}

func TestNotLoggedIn(t *testing.T) {
	c := New("http://unused")
	_, err := c.DeviceToken(context.Background())
	assert.ErrorIs(t, err, config.ErrNotLoggedIn)
}
