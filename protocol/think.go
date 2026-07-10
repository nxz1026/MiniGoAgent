package protocol

import "strings"

type thinkState int

const (
	thinkProbe thinkState = iota
	thinkInside
	thinkPassthrough
)

type thinkSplitter struct {
	state thinkState
	buf   strings.Builder
	depth int
}

func (s *thinkSplitter) push(text string) (thought, visible string) {
	switch s.state {
	case thinkProbe:
		if tag := extractThinkTag(text); tag != "" {
			before, after, ok := strings.Cut(text, tag)
			s.depth++
			s.state = thinkInside
			visible = before
			if ok {
				s.extractAndEmit(after, &thought, &visible)
			}
			return
		}
		if idx := strings.Index(text, "<think"); idx >= 0 {
			before := text[:idx]
			rest := text[idx:]
			visible = before
			s.state = thinkInside
			s.depth++
			inner := rest[len("<think"):]
			if strings.HasPrefix(inner, ">") {
				s.extractAndEmit(inner[1:], &thought, &visible)
			} else {
				s.buf.WriteString(inner)
			}
			return
		}
		s.state = thinkPassthrough
		visible = text
	case thinkInside:
		before, after, ok := strings.Cut(text, "</think>")
		if ok {
			s.depth--
			if s.depth == 0 {
				s.state = thinkProbe
			}
			if s.buf.Len() > 0 {
				s.buf.WriteString(before)
				thought = s.buf.String()
				s.buf.Reset()
			} else {
				thought = before
			}
			visible = after
			return
		}
		s.buf.WriteString(text)
	case thinkPassthrough:
		visible = text
	}
	return
}

func (s *thinkSplitter) extractAndEmit(text string, thought, visible *string) {
	before, after, ok := strings.Cut(text, "</think>")
	if ok {
		s.depth--
		if s.depth == 0 {
			s.state = thinkProbe
		}
		if s.buf.Len() > 0 {
			s.buf.WriteString(before)
			*thought = s.buf.String()
			s.buf.Reset()
		} else {
			*thought = before
		}
		*visible += after
		return
	}
	s.buf.WriteString(text)
}

func (s *thinkSplitter) flush() (thought, visible string) {
	if s.buf.Len() > 0 {
		thought = s.buf.String()
		s.buf.Reset()
	}
	s.state = thinkProbe
	s.depth = 0
	return
}

func extractThinkTag(text string) string {
	if strings.HasPrefix(text, "<think>") {
		return "<think>"
	}
	if rest, ok := strings.CutPrefix(text, "<think "); ok {
		end := strings.IndexByte(rest, '>')
		if end >= 0 {
			return text[:len(text)-len(rest)+end+1]
		}
	}
	return ""
}

func (s *thinkSplitter) extractThink(text string) string {
	before, _, ok := strings.Cut(text, "</think>")
	if ok {
		return before
	}
	s.buf.WriteString(text)
	return ""
}
