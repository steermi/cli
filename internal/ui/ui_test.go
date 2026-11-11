package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlainWhenNotTerminal(t *testing.T) {
	s := For(&bytes.Buffer{})
	assert.Equal(t, "x", s.Bold("x"))
	assert.Equal(t, "✓", s.Mark("ok"))
}

func TestColorWraps(t *testing.T) {
	s := Style{color: true}
	assert.Equal(t, "\x1b[1mx\x1b[0m", s.Bold("x"))
	assert.Equal(t, "", s.Bold(""))
}

func TestPadClip(t *testing.T) {
	assert.Equal(t, "ab  ", Pad("ab", 4))
	assert.Equal(t, "abc…", Clip("abcdef", 4))
	assert.Equal(t, "ab", Clip("ab", 4))
}

func TestPrompterSharesBuffer(t *testing.T) {
	var out bytes.Buffer
	p := NewPrompter(strings.NewReader("one\n\nthree\n"), &out)
	a, err := p.Ask("? ", "")
	require.NoError(t, err)
	b, _ := p.Ask("? ", "def")
	c, _ := p.Ask("? ", "")
	assert.Equal(t, []string{"one", "def", "three"}, []string{a, b, c})
}
