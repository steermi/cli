// Package deeplink registers and handles steermi://launch/<id> links, so the
// "Open in terminal" button in Steermi starts `steermi run` in one click
// (docs/architecture.md).
package deeplink

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var linkPattern = regexp.MustCompile(`^steermi://launch/(lx_[A-Za-z0-9]{10})/?$`)

// Parse validates a link and returns its launch ID. Anything but the exact
// expected shape is rejected, so link contents never reach a shell.
func Parse(link string) (string, error) {
	m := linkPattern.FindStringSubmatch(strings.TrimSpace(link))
	if m == nil {
		return "", errors.New("not a steermi launch link")
	}
	return m[1], nil
}

// appleQuote quotes s as an AppleScript string literal.
func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// HandlerScript is the AppleScript applet body: it hands the URL to
// `steermi open-terminal`, which validates it before doing anything.
func HandlerScript(exe string) string {
	return "on open location theURL\n" +
		"\tdo shell script " + appleQuote(shellQuote(exe)+" open-terminal ") + " & quoted form of theURL\n" +
		"end open location\n"
}

// TerminalScript opens a terminal window running `steermi open <link>`.
// link must already be validated by Parse.
func TerminalScript(app, exe, link string) string {
	cmd := shellQuote(exe) + " open " + shellQuote(link)
	if app == "iTerm" || app == "iTerm2" {
		return "tell application \"iTerm\"\n\tactivate\n\tcreate window with default profile command " + appleQuote(cmd) + "\nend tell\n"
	}
	return "tell application \"Terminal\"\n\tactivate\n\tdo script " + appleQuote(cmd) + "\nend tell\n"
}

// DesktopEntry is the Linux .desktop file registering the scheme.
func DesktopEntry(exe string) string {
	return "[Desktop Entry]\nType=Application\nName=Steermi Link\nNoDisplay=true\n" +
		"Exec=" + strings.ReplaceAll(exe, " ", `\s`) + " open-terminal %u\n" +
		"MimeType=x-scheme-handler/steermi;\n"
}

// Register installs the handler for the current platform.
func Register(exe string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return registerDarwin(exe)
	case "linux":
		return registerLinux(exe)
	}
	return "", fmt.Errorf("steermi:// links are not supported on %s yet — copy the command from Steermi instead", runtime.GOOS)
}

func registerDarwin(exe string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	app := filepath.Join(home, "Applications", "Steermi Link.app")
	if err := os.MkdirAll(filepath.Dir(app), 0o755); err != nil {
		return "", err
	}
	_ = os.RemoveAll(app)
	if out, err := exec.Command("osacompile", "-o", app, "-e", HandlerScript(exe)).CombinedOutput(); err != nil {
		return "", fmt.Errorf("osacompile: %v: %s", err, out)
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	for _, cmd := range []string{
		"Add :CFBundleIdentifier string com.steermi.link",
		"Add :CFBundleURLTypes array",
		"Add :CFBundleURLTypes:0 dict",
		"Add :CFBundleURLTypes:0:CFBundleURLName string Steermi launch link",
		"Add :CFBundleURLTypes:0:CFBundleURLSchemes array",
		"Add :CFBundleURLTypes:0:CFBundleURLSchemes:0 string steermi",
		"Add :LSUIElement bool true",
	} {
		_ = exec.Command("/usr/libexec/PlistBuddy", "-c", cmd, plist).Run()
	}
	lsregister := "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
	if out, err := exec.Command(lsregister, "-f", app).CombinedOutput(); err != nil {
		return "", fmt.Errorf("register handler: %v: %s", err, out)
	}
	return fmt.Sprintf("Registered steermi:// links (%s). Set STEERMI_TERMINAL=iTerm to open iTerm instead of Terminal.", app), nil
}

func registerLinux(exe string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "steermi-link.desktop"), []byte(DesktopEntry(exe)), 0o644); err != nil {
		return "", err
	}
	if out, err := exec.Command("xdg-mime", "default", "steermi-link.desktop", "x-scheme-handler/steermi").CombinedOutput(); err != nil {
		return "", fmt.Errorf("xdg-mime: %v: %s", err, out)
	}
	return "Registered steermi:// links. Set TERMINAL to choose the terminal emulator.", nil
}

// OpenTerminal opens a terminal running `steermi open <link>`.
func OpenTerminal(exe, link string) error {
	if _, err := Parse(link); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		app := os.Getenv("STEERMI_TERMINAL")
		return exec.Command("osascript", "-e", TerminalScript(app, exe, link)).Run()
	case "linux":
		term := os.Getenv("TERMINAL")
		if term == "" {
			term = "x-terminal-emulator"
		}
		return exec.Command(term, "-e", exe, "open", link).Start()
	}
	return fmt.Errorf("unsupported platform %s", runtime.GOOS)
}
