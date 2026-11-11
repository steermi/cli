package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// lockPoll is how often a waiting process retries a held lock.
const lockPoll = 25 * time.Millisecond

// stateLockTimeout bounds waiting for a launch state update; holders keep
// that lock only for a read-modify-write of one small file.
const stateLockTimeout = 10 * time.Second

// errLocked is returned by tryLock when another holder has the lock.
var errLocked = errors.New("locked")

// lockFile takes an exclusive advisory lock on p (created 0600), waiting
// until ctx is done. Locks are per open file, so two goroutines of one
// process exclude each other as well as separate processes do.
func lockFile(ctx context.Context, p string) (func(), error) {
	if err := ensureDir(p); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := tryLock(f)
		if err == nil {
			return func() {
				_ = unlock(f)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, errLocked) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", p, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", p, ctx.Err())
		case <-time.After(lockPoll):
		}
	}
}

func launchLockFile(launchID, suffix string) (string, error) {
	p, err := launchFile(launchID)
	if err != nil {
		return "", err
	}
	return p + suffix, nil
}

// LockLaunchMirror serializes transcript mirroring for a launch across the
// agent hooks, the run heartbeat and `steermi done`/`handoff`. It is a
// separate lock from the state lock, so a mirror holder may still update
// launch state (offset, refreshed token) through UpdateLaunch.
func LockLaunchMirror(ctx context.Context, launchID string) (func(), error) {
	p, err := launchLockFile(launchID, ".mirror.lock")
	if err != nil {
		return nil, err
	}
	return lockFile(ctx, p)
}

// UpdateLaunch applies fn to the stored launch state under the launch's
// state lock and returns the saved state. fn sees what other processes last
// wrote, so concurrent updates of different fields never overwrite each
// other. A missing state starts empty.
func UpdateLaunch(launchID string, fn func(*LaunchState)) (*LaunchState, error) {
	p, err := launchFile(launchID)
	if err != nil {
		return nil, err
	}
	lp, err := launchLockFile(launchID, ".lock")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), stateLockTimeout)
	defer cancel()
	unlock, err := lockFile(ctx, lp)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s := &LaunchState{}
	if err := readJSON(p, s); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read launch state: %w", err)
	}
	s.upgrade()
	fn(s)
	s.LaunchID = launchID
	if err := writeJSON(p, s); err != nil {
		return nil, err
	}
	return s, nil
}
