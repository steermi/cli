package buildinfo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetUsesLinkerValues(t *testing.T) {
	prevV, prevC, prevD := version, commit, date
	t.Cleanup(func() { version, commit, date = prevV, prevC, prevD })

	version, commit, date = "v1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-10-27T10:00:00Z"
	i := Get()
	assert.Equal(t, "v1.2.3", i.Version)
	assert.Equal(t, "0123456789ab", i.Commit, "commits are shortened")
	assert.True(t, strings.HasPrefix(i.String(), "steermi v1.2.3 (0123456789ab, 2026-10-27T10:00:00Z) go"))
	assert.Contains(t, UserAgent(), "steermi-cli/v1.2.3 (")
}

func TestGetFallsBackToDev(t *testing.T) {
	prevV, prevC := version, commit
	t.Cleanup(func() { version, commit = prevV, prevC })

	version, commit = "", "abc"
	i := Get()
	assert.NotEmpty(t, i.Version, "test binaries report dev or the module version")
	assert.Equal(t, "abc", i.Commit[:3])
}
