package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/cli/internal/api"
	"github.com/steermi/cli/internal/config"
)

// mirrorServer stores mirrored messages by external ID, like Steermi does.
type mirrorServer struct {
	mu       sync.Mutex
	posts    int
	failPost int // 1-based POST number that fails once
	stored   map[string]string
	order    []string
}

func (s *mirrorServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts++
	if s.posts == s.failPost {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"UNAVAILABLE","message":"try again"}}`))
		return
	}
	var body struct {
		Messages []struct {
			Content    string `json:"content"`
			ExternalID string `json:"external_id"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	for _, m := range body.Messages {
		if _, ok := s.stored[m.ExternalID]; !ok {
			s.order = append(s.order, m.ExternalID)
		}
		s.stored[m.ExternalID] = m.Content
	}
	_, _ = w.Write([]byte(`{"data":{"messages":[]}}`))
}

func flushFixture(t *testing.T, srv *mirrorServer, turns int) (func() *Active, string) {
	t.Helper()
	srv.stored = map[string]string{}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	cfgDir := t.TempDir()
	prev := config.Dir
	config.Dir = func() (string, error) { return cfgDir, nil }
	t.Cleanup(func() { config.Dir = prev })

	state := &config.LaunchState{
		LaunchID: "6f0c7a5e-1d4b-4c55-9a3e-8b7f2d1c0e9a", SessionID: "s1", ClaudeSessionID: "cs-1",
		LaunchToken: "launch-token", TokenExpiry: time.Now().Add(time.Hour),
	}
	require.NoError(t, config.SaveLaunch(state))

	var b strings.Builder
	for i := 0; i < turns; i++ {
		fmt.Fprintf(&b, `{"type":"user","uuid":"u-%d","message":{"role":"user","content":"ask %d"}}`+"\n", i, i)
		fmt.Fprintf(&b, `{"type":"assistant","uuid":"a-%d","message":{"role":"assistant","content":[{"type":"text","text":"answer %d"}]}}`+"\n", i, i)
	}
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte(b.String()), 0o600))

	client := api.New(ts.URL)
	// Each caller (hook process, heartbeat) loads its own copy of the state.
	return func() *Active {
		st, err := config.LoadLaunch(state.LaunchID)
		require.NoError(t, err)
		return &Active{State: st, Client: client, Now: time.Now}
	}, transcript
}

func TestFlushResumesAfterAFailedBatch(t *testing.T) {
	srv := &mirrorServer{failPost: 2}
	active, transcript := flushFixture(t, srv, 15) // 30 messages: batches of 20 + 10

	err := Flush(context.Background(), active(), transcript)
	require.Error(t, err, "the second batch fails")
	st, err := config.LoadLaunch("6f0c7a5e-1d4b-4c55-9a3e-8b7f2d1c0e9a")
	require.NoError(t, err)
	assert.Equal(t, 20, st.MirrorOffset, "the stored first batch advances the offset")

	require.NoError(t, Flush(context.Background(), active(), transcript))
	assert.Equal(t, 3, srv.posts, "the retry sends only the remaining batch")
	assert.Len(t, srv.stored, 30)
	assert.Equal(t, "cc:u-0", srv.order[0])
	assert.Equal(t, "answer 14", srv.stored["cc:a-14"])
}

func TestConcurrentFlushesDoNotDuplicate(t *testing.T) {
	srv := &mirrorServer{}
	active, transcript := flushFixture(t, srv, 30)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, Flush(context.Background(), active(), transcript))
		}()
	}
	wg.Wait()
	assert.Len(t, srv.stored, 60)
	assert.Equal(t, 3, srv.posts, "flushes run one at a time from the stored offset")
	st, err := config.LoadLaunch("6f0c7a5e-1d4b-4c55-9a3e-8b7f2d1c0e9a")
	require.NoError(t, err)
	assert.Equal(t, 60, st.MirrorOffset)
}

func TestUpdateLaunchKeepsOtherWriters(t *testing.T) {
	srv := &mirrorServer{}
	active, transcript := flushFixture(t, srv, 1)
	stale := active()

	// A token refresh with a stale in-memory copy must not roll back the
	// offset another process stored.
	require.NoError(t, Flush(context.Background(), active(), transcript))
	_, err := config.UpdateLaunch(stale.State.LaunchID, func(s *config.LaunchState) { s.LaunchToken = "fresh" })
	require.NoError(t, err)
	st, err := config.LoadLaunch(stale.State.LaunchID)
	require.NoError(t, err)
	assert.Equal(t, 2, st.MirrorOffset)
	assert.Equal(t, "fresh", st.LaunchToken)
}
