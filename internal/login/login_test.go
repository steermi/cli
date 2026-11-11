package login

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalURL(t *testing.T) {
	u, err := url.Parse(ApprovalURL(Request{BaseURL: "https://steermi.com/", Name: "mk mbp", Hostname: "mbp.local", OS: "darwin"}, "chal", "st", 4321))
	require.NoError(t, err)
	assert.Equal(t, "/device", u.Path)
	assert.Equal(t, "chal", u.Query().Get("challenge"))
	assert.Equal(t, "4321", u.Query().Get("port"))
	assert.Equal(t, "mk mbp", u.Query().Get("name"))
}

// A slow browser connection must receive the complete response before shutdown.
type slowResponseListener struct{ net.Listener }

func (l slowResponseListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return slowResponseConn{c}, nil
}

type slowResponseConn struct{ net.Conn }

func (c slowResponseConn) Write(p []byte) (int, error) {
	time.Sleep(20 * time.Millisecond)
	return c.Conn.Write(p)
}

func TestWaitForCode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := "http://" + ln.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := make(chan string, 1)
	go func() {
		code, err := WaitForCode(ctx, slowResponseListener{ln}, "good-state")
		assert.NoError(t, err)
		result <- code
	}()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(addr + "/callback?state=evil&code=stolen")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "wrong state is rejected")

	resp, err = http.Get(addr + "/callback?state=good-state&code=stp_ok")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "Device paired")
	assert.Equal(t, "stp_ok", <-result)
}

func TestWaitForCodeTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = WaitForCode(ctx, ln, "s")
	assert.ErrorContains(t, err, "timed out")
}

func TestPaste(t *testing.T) {
	var out bytes.Buffer
	res, err := Paste(Request{BaseURL: "https://steermi.com"}, strings.NewReader("  stp_code \n"), &out)
	require.NoError(t, err)
	assert.Equal(t, "stp_code", res.Code)
	assert.Len(t, res.Verifier, 43)
	assert.Contains(t, out.String(), "port=0")

	_, err = Paste(Request{BaseURL: "https://steermi.com"}, strings.NewReader("\n"), &out)
	assert.Error(t, err)
}
