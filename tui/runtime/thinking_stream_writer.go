package tuiruntime

import (
	"io"
	"strings"
)

const (
	thinkStartTag = "<think>"
	thinkEndTag   = "</think>"
	ansiGray      = "\x1b[90m"
	ansiReset     = "\x1b[0m"
)

type thinkingStreamWriter struct {
	out            io.Writer
	preview        *TerminalPreview
	mode           thinkingStreamMode
	tagBuf         strings.Builder
	normalStarted  bool
	previewStarted bool
}

type thinkingStreamMode int

const (
	thinkingModeNormal thinkingStreamMode = iota
	thinkingModeStartTag
	thinkingModeBody
	thinkingModeEndTag
)

func newThinkingStreamWriter(out io.Writer, lines int, width int) *thinkingStreamWriter {
	return &thinkingStreamWriter{
		out:     out,
		preview: NewStyledTerminalPreview(out, lines, width, ansiGray, ansiReset),
	}
}

func (w *thinkingStreamWriter) Write(data []byte) (int, error) {
	for _, r := range string(data) {
		if err := w.writeRune(r); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (w *thinkingStreamWriter) Close() error {
	switch w.mode {
	case thinkingModeStartTag:
		if err := w.writeNormal(w.tagBuf.String()); err != nil {
			return err
		}
	case thinkingModeEndTag:
		if err := w.writeThinking(w.tagBuf.String()); err != nil {
			return err
		}
	}
	if w.previewStarted {
		if err := w.preview.End(); err != nil {
			return err
		}
	}
	w.tagBuf.Reset()
	w.mode = thinkingModeNormal
	w.previewStarted = false
	return nil
}

func (w *thinkingStreamWriter) writeRune(r rune) error {
	switch w.mode {
	case thinkingModeNormal:
		if !w.normalStarted && r == '<' {
			w.mode = thinkingModeStartTag
			w.tagBuf.Reset()
			w.tagBuf.WriteRune(r)
			return nil
		}
		return w.writeNormal(string(r))
	case thinkingModeStartTag:
		return w.consumeTagRune(r, thinkStartTag, thinkingModeBody, w.writeNormal, func() error {
			if err := w.preview.Begin(); err != nil {
				return err
			}
			w.previewStarted = true
			return nil
		})
	case thinkingModeBody:
		if r == '<' {
			w.mode = thinkingModeEndTag
			w.tagBuf.Reset()
			w.tagBuf.WriteRune(r)
			return nil
		}
		return w.writeThinking(string(r))
	case thinkingModeEndTag:
		return w.consumeTagRune(r, thinkEndTag, thinkingModeNormal, w.writeThinking, func() error {
			if err := w.preview.End(); err != nil {
				return err
			}
			w.previewStarted = false
			return nil
		})
	default:
		return nil
	}
}

func (w *thinkingStreamWriter) consumeTagRune(r rune, fullTag string, nextMode thinkingStreamMode, fallback func(string) error, onMatch func() error) error {
	w.tagBuf.WriteRune(r)
	current := w.tagBuf.String()
	if strings.HasPrefix(fullTag, current) {
		if current == fullTag {
			w.tagBuf.Reset()
			w.mode = nextMode
			return onMatch()
		}
		return nil
	}
	fallbackText := current
	w.tagBuf.Reset()
	if err := fallback(fallbackText); err != nil {
		return err
	}
	if nextMode == thinkingModeBody {
		w.mode = thinkingModeNormal
	} else {
		w.mode = thinkingModeBody
	}
	return nil
}

func (w *thinkingStreamWriter) writeNormal(text string) error {
	if text == "" {
		return nil
	}
	w.normalStarted = true
	_, err := io.WriteString(w.out, text)
	return err
}

func (w *thinkingStreamWriter) writeThinking(text string) error {
	if text == "" {
		return nil
	}
	_, err := io.WriteString(w.preview, text)
	return err
}
