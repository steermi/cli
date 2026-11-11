package deeplink

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	id, err := Parse("steermi://launch/lx_7Q4XK2M9ab")
	require.NoError(t, err)
	assert.Equal(t, "lx_7Q4XK2M9ab", id)
	_, err = Parse("steermi://launch/lx_7Q4XK2M9ab/")
	assert.NoError(t, err)

	for _, bad := range []string{
		"",
		"steermi://launch/lx_short",
		"steermi://launch/lx_7Q4XK2M9ab;rm -rf ~",
		"steermi://launch/lx_7Q4XK2M9ab$(touch /tmp/x)",
		"steermi://launch/lx_7Q4XK2M9ab?x=1",
		"steermi://launch/../../etc/passwd",
		"https://steermi.com/launch/lx_7Q4XK2M9ab",
		"steermi://launch/lx_7Q4XK2M9ab\"; do shell script \"id",
		"steermi://LAUNCH/lx_7Q4XK2M9ab",
	} {
		_, err := Parse(bad)
		assert.Error(t, err, bad)
	}
}

func TestHandlerScriptQuotesExecutable(t *testing.T) {
	s := HandlerScript(`/Users/a b/bin/steer"mi`)
	assert.Contains(t, s, "on open location theURL")
	assert.Contains(t, s, "quoted form of theURL", "the URL is passed as one shell word")
	assert.Contains(t, s, `'/Users/a b/bin/steer\"mi'`)
}

func TestTerminalScripts(t *testing.T) {
	link := "steermi://launch/lx_7Q4XK2M9ab"
	term := TerminalScript("", "/usr/local/bin/steermi", link)
	assert.Contains(t, term, `tell application "Terminal"`)
	assert.Contains(t, term, `do script "'/usr/local/bin/steermi' open 'steermi://launch/lx_7Q4XK2M9ab'"`)
	iterm := TerminalScript("iTerm", "/usr/local/bin/steermi", link)
	assert.Contains(t, iterm, `tell application "iTerm"`)
}

func TestDesktopEntry(t *testing.T) {
	d := DesktopEntry("/opt/steermi tools/steermi")
	assert.Contains(t, d, `Exec=/opt/steermi\stools/steermi open-terminal %u`)
	assert.True(t, strings.Contains(d, "MimeType=x-scheme-handler/steermi;"))
}

func TestOpenTerminalRejectsBadLinks(t *testing.T) {
	assert.Error(t, OpenTerminal("/bin/steermi", "steermi://launch/x;id"))
}
