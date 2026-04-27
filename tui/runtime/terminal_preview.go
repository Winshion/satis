package tuiruntime

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mattn/go-runewidth"
)

type TerminalPreview struct {
	out    io.Writer
	lines  int
	width  int
	mu     sync.Mutex
	rows   []string
	row    int
	active bool
	style  string
	reset  string
}

func NewTerminalPreview(out io.Writer, lines int, width int) *TerminalPreview {
	return NewStyledTerminalPreview(out, lines, width, "", "")
}

func NewStyledTerminalPreview(out io.Writer, lines int, width int, style string, reset string) *TerminalPreview {
	if lines <= 0 {
		lines = 3
	}
	if width <= 0 {
		width = 80
	}
	return &TerminalPreview{
		out:   out,
		lines: lines,
		width: width,
		rows:  make([]string, lines),
		style: style,
		reset: reset,
	}
}

func (p *TerminalPreview) Begin() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.rows = make([]string, p.lines)
	p.row = 0
	p.active = true

	for i := 0; i < p.lines; i++ {
		if _, err := fmt.Fprintln(p.out); err != nil {
			return err
		}
	}
	return nil
}

func (p *TerminalPreview) End() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.active {
		return nil
	}
	if _, err := fmt.Fprintf(p.out, "\x1b[%dA\r", p.lines); err != nil {
		return err
	}
	for i := 0; i < p.lines; i++ {
		if _, err := fmt.Fprint(p.out, "\x1b[2K"); err != nil {
			return err
		}
		if i < p.lines-1 {
			if _, err := fmt.Fprint(p.out, "\n"); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintf(p.out, "\x1b[%dA\r", p.lines-1); err != nil {
		return err
	}
	p.active = false
	return nil
}

func (p *TerminalPreview) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, r := range string(data) {
		switch r {
		case '\r':
			continue
		case '\n':
			p.advanceLine()
		default:
			if runewidth.StringWidth(p.rows[p.row])+runewidth.RuneWidth(r) > p.width {
				p.advanceLine()
			}
			p.rows[p.row] += string(r)
		}
	}
	if p.active {
		if err := p.renderLocked(); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (p *TerminalPreview) advanceLine() {
	p.row++
	if p.row >= p.lines {
		p.row = 0
	}
	p.rows[p.row] = ""
}

func (p *TerminalPreview) renderLocked() error {
	if _, err := fmt.Fprintf(p.out, "\x1b[%dA\r", p.lines); err != nil {
		return err
	}
	for i := 0; i < p.lines; i++ {
		if _, err := fmt.Fprint(p.out, "\x1b[2K"); err != nil {
			return err
		}
		line := strings.TrimRight(p.rows[i], "\n")
		if p.style != "" && line != "" {
			line = p.style + line + p.reset
		}
		if _, err := fmt.Fprint(p.out, line); err != nil {
			return err
		}
		if _, err := fmt.Fprint(p.out, "\n"); err != nil {
			return err
		}
	}
	return nil
}
