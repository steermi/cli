// Package ui is the CLI's small output layer: TTY detection, a handful of
// styles and aligned key/value rows. Colour is used only on terminals and
// never when NO_COLOR or STEERMI_NO_COLOR is set, so piped output stays
// plain text.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Style renders text for one writer.
type Style struct{ color bool }

// For returns the style for w: colour only on a terminal.
func For(w io.Writer) Style {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("STEERMI_NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return Style{}
	}
	return Style{color: IsTerminal(w)}
}

// IsTerminal reports whether v (a writer or reader) is a character device.
func IsTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (s Style) wrap(code, text string) string {
	if !s.color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(t string) string   { return s.wrap("1", t) }
func (s Style) Dim(t string) string    { return s.wrap("2", t) }
func (s Style) Accent(t string) string { return s.wrap("36", t) }
func (s Style) Good(t string) string   { return s.wrap("32", t) }
func (s Style) Warn(t string) string   { return s.wrap("33", t) }
func (s Style) Bad(t string) string    { return s.wrap("31", t) }

// Mark is the status glyph for ok / warn / problem rows.
func (s Style) Mark(kind string) string {
	switch kind {
	case "ok":
		return s.Good("✓")
	case "warn":
		return s.Warn("!")
	}
	return s.Bad("✗")
}

// Rule is a dim horizontal line of width n.
func (s Style) Rule(n int) string { return s.Dim(strings.Repeat("─", n)) }

// Pad right-pads plain text to n display columns (rune count).
func Pad(t string, n int) string {
	if l := utf8.RuneCountInString(t); l < n {
		return t + strings.Repeat(" ", n-l)
	}
	return t
}

// Clip shortens t to n runes with an ellipsis.
func Clip(t string, n int) string {
	if r := []rune(t); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return t
}

// Row prints "  label  value" with the label padded to width.
func (s Style) Row(w io.Writer, label string, width int, value string) {
	fmt.Fprintf(w, "   %s %s\n", s.Dim(Pad(label, width)), value)
}

// ErrCancelled is returned when the developer quits a prompt.
var ErrCancelled = errors.New("cancelled")

// Prompter reads one-line answers. A single reader is shared so buffered
// input is never lost between questions.
type Prompter struct {
	r   *bufio.Reader
	out io.Writer
}

// NewPrompter reads answers from in and writes questions to out.
func NewPrompter(in io.Reader, out io.Writer) *Prompter {
	if br, ok := in.(*bufio.Reader); ok {
		return &Prompter{r: br, out: out}
	}
	return &Prompter{r: bufio.NewReader(in), out: out}
}

// Ask prints question and returns the trimmed answer (def when empty).
func (p *Prompter) Ask(question, def string) (string, error) {
	fmt.Fprint(p.out, question)
	line, err := p.r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if errors.Is(err, io.EOF) && line == "" {
		return def, io.EOF
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}
