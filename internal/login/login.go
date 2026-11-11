// Package login pairs the steermi CLI with a Steermi account: the CLI opens
// the browser on /device with a PKCE challenge, the signed-in user approves,
// and the one-time code comes back over a loopback callback (or is pasted).
package login

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/steermi/agent-contracts/locallaunch"
)

// Timeout bounds how long login waits for the browser approval.
const Timeout = 5 * time.Minute

// Request describes the device being paired.
type Request struct {
	BaseURL  string
	Name     string
	Hostname string
	OS       string
}

// Result is the approved pairing.
type Result struct {
	Code     string
	Verifier string
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ApprovalURL builds the browser URL. port 0 asks the page to show the code
// for pasting instead of redirecting.
func ApprovalURL(r Request, challenge, state string, port int) string {
	q := url.Values{}
	q.Set("challenge", challenge)
	q.Set("state", state)
	q.Set("port", strconv.Itoa(port))
	q.Set("name", r.Name)
	q.Set("hostname", r.Hostname)
	q.Set("os", r.OS)
	return strings.TrimRight(r.BaseURL, "/") + "/device?" + q.Encode()
}

// Browser runs the loopback flow. out receives user-facing messages.
func Browser(ctx context.Context, r Request, out io.Writer) (*Result, error) {
	verifier, err := randomString(32)
	if err != nil {
		return nil, err
	}
	state, err := randomString(16)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start login callback: %w", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	link := ApprovalURL(r, locallaunch.PKCEChallenge(verifier), state, port)

	fmt.Fprintf(out, "Opening your browser to approve this device…\nIf it does not open, visit:\n\n  %s\n\n", link)
	_ = OpenBrowser(link)

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	code, err := WaitForCode(ctx, ln, state)
	if err != nil {
		return nil, err
	}
	return &Result{Code: code, Verifier: verifier}, nil
}

// Paste runs the copy-the-code flow for machines without a browser.
func Paste(r Request, in io.Reader, out io.Writer) (*Result, error) {
	verifier, err := randomString(32)
	if err != nil {
		return nil, err
	}
	state, err := randomString(16)
	if err != nil {
		return nil, err
	}
	link := ApprovalURL(r, locallaunch.PKCEChallenge(verifier), state, 0)
	fmt.Fprintf(out, "Open this link on any device where you are signed in to Steermi:\n\n  %s\n\nThen paste the code shown there: ", link)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	code := strings.TrimSpace(line)
	if code == "" {
		return nil, errors.New("no code entered")
	}
	return &Result{Code: code, Verifier: verifier}, nil
}

// WaitForCode serves /callback on ln until a request with the right state
// arrives. Requests with another state are rejected and do not end the wait.
func WaitForCode(ctx context.Context, ln net.Listener, state string) (string, error) {
	codes := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state || q.Get("code") == "" {
			http.Error(w, "This login link does not match the running `steermi login`.", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Steermi</title><body style="font-family:system-ui;padding:3rem"><h1>Device paired</h1><p>%s</p><p>You can close this tab and return to your terminal.</p></body>`, html.EscapeString("steermi is now connected to your account."))
		select {
		case codes <- q.Get("code"):
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	select {
	case code := <-codes:
		// Let the callback handler finish and flush its response before closing
		// the listener. Close alone can reset the browser's connection.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return code, nil
	case <-ctx.Done():
		return "", errors.New("timed out waiting for browser approval — run `steermi login` again")
	}
}

// OpenBrowser opens a URL with the platform's default browser.
func OpenBrowser(link string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no display")
		}
		cmd = exec.Command("xdg-open", link)
	}
	return cmd.Start()
}
