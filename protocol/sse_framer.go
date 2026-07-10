//go:build never

// NOT WIRED: SseFramer is a byte-level SSE frame parser designed for raw TCP
// streams (transparent proxy / reverse proxy), where TCP segments can split
// frames at any byte boundary. MiniGoAgent is an HTTP client — Go's net/http
// handles HTTP chunked transfer transparently and bufio.Scanner in readStream()
// already provides safe line-level buffering. Switching to SseFramer would add
// complexity for zero actual benefit.
//
// To wire: replace bufio.Scanner loop in OpenAI.readStream() with:
//
//	framer := NewSseFramer()
//	framer.Push(chunk)
//	for {
//	    evt, err := framer.NextEvent()
//	    if err != nil { ... }
//	    if evt == nil { break }
//	    // handle evt.Data
//	}
//
// EventStreamParser is a binary frame parser for AWS Bedrock's
// application/vnd.amazon.eventstream protocol. NOT WIRED because MiniGoAgent
// does not yet support Bedrock as a vendor. Wire when adding Bedrock
// support: replace the SSE-parsing path in readStream() with EventStreamParser
// loop that reads binary frames, extracts payload (UTF-8 JSON), and feeds
// them through the existing Chunk pipeline.

package protocol

import (
	"bytes"
	"fmt"
)

type SseEvent struct {
	Event string
	Data  []byte
}

type SseFramer struct {
	buf  bytes.Buffer
	done bool
	idle int
}

func NewSseFramer() *SseFramer {
	return &SseFramer{}
}

func (f *SseFramer) Push(chunk []byte) {
	if len(chunk) == 0 || f.done {
		return
	}
	f.buf.Write(chunk)
}

func (f *SseFramer) NextEvent() (*SseEvent, error) {
	// scan for \n\n boundary
	data := f.buf.Bytes()
	end := findDoubleNewline(data)
	if end < 0 {
		return nil, nil
	}
	// extract one complete event
	block := make([]byte, end)
	copy(block, data[:end])
	// advance buffer past the \n\n
	skip := end
	for skip < len(data) && (data[skip] == '\n' || data[skip] == '\r') {
		skip++
	}
	f.buf.Next(skip)

	evt, err := parseSSEBlock(block)
	if err != nil {
		return nil, err
	}
	if evt == nil {
		return nil, nil
	}
	return evt, nil
}

func (f *SseFramer) BufferedLen() int {
	return f.buf.Len()
}

func (f *SseFramer) Bytes() []byte {
	return f.buf.Bytes()
}

func (f *SseFramer) Reset() {
	f.buf.Reset()
	f.done = false
}

func findDoubleNewline(data []byte) int {
	for i := 0; i < len(data)-1; i++ {
		if data[i] == '\n' {
			// \n\n
			if i+1 < len(data) && data[i+1] == '\n' {
				return i
			}
			// \n\r\n
			if i+2 < len(data) && data[i+1] == '\r' && data[i+2] == '\n' {
				return i
			}
		}
		// \r\n\r\n
		if data[i] == '\r' && i+3 < len(data) && data[i+1] == '\n' && data[i+2] == '\r' && data[i+3] == '\n' {
			return i
		}
	}
	return -1
}

func parseSSEBlock(block []byte) (*SseEvent, error) {
	lines := bytes.Split(block, []byte("\n"))
	if len(lines) == 0 {
		return nil, nil
	}
	evt := &SseEvent{}
	var dataLines [][]byte
	for _, line := range lines {
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			continue
		}
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			val := bytes.TrimSpace(line[6:])
			evt.Event = string(val)
		case bytes.HasPrefix(line, []byte("data:")):
			val := bytes.TrimSpace(line[5:])
			dataLines = append(dataLines, val)
		case bytes.HasPrefix(line, []byte("id:")):
			// ignore, not needed
		case bytes.HasPrefix(line, []byte("retry:")):
			// ignore
		default:
			// comment or unknown field - skip
		}
	}
	if len(dataLines) == 0 {
		return nil, nil
	}
	evt.Data = bytes.Join(dataLines, []byte("\n"))
	return evt, nil
}

func IsSseDone(data []byte) bool {
	return string(bytes.TrimSpace(data)) == "[DONE]"
}

// EventStream binary frame parser — Bedrock
type EventStreamMsg struct {
	Headers map[string]string
	Payload []byte
}

type EventStreamParser struct {
	buf     bytes.Buffer
	maxSize int
}

func NewEventStreamParser(maxSize int) *EventStreamParser {
	if maxSize <= 0 {
		maxSize = 32 * 1024 * 1024
	}
	return &EventStreamParser{maxSize: maxSize}
}

const preludeLen = 12
const msgCrcLen = 4

func (p *EventStreamParser) Push(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	p.buf.Write(chunk)
}

func (p *EventStreamParser) NextMessage() (*EventStreamMsg, error) {
	buf := p.buf.Bytes()
	if len(buf) < preludeLen {
		return nil, nil
	}
	totalLen := readBE32(buf[0:4])
	headersLen := readBE32(buf[4:8])
	if int(totalLen) > p.maxSize {
		return nil, fmt.Errorf("eventstream message too large: %d > %d", totalLen, p.maxSize)
	}
	minLen := preludeLen + int(headersLen) + msgCrcLen
	if int(totalLen) < minLen {
		return nil, fmt.Errorf("implausible eventstream lengths: total=%d headers=%d", totalLen, headersLen)
	}
	if len(buf) < int(totalLen) {
		return nil, nil
	}
	// prelude CRC validation
	_ = readBE32(buf[8:12]) // prelude_crc - we skip validation for now

	headersEnd := preludeLen + int(headersLen)
	payloadEnd := int(totalLen) - msgCrcLen

	headersRaw := buf[preludeLen:headersEnd]
	payload := make([]byte, payloadEnd-headersEnd)
	copy(payload, buf[headersEnd:payloadEnd])

	// advance buffer
	p.buf.Next(int(totalLen))

	headers := parseEventStreamHeaders(headersRaw)
	return &EventStreamMsg{Headers: headers, Payload: payload}, nil
}

func (p *EventStreamParser) BufferedLen() int {
	return p.buf.Len()
}

func readBE32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func parseEventStreamHeaders(raw []byte) map[string]string {
	h := make(map[string]string)
	for i := 0; i < len(raw); {
		if i+1 > len(raw) {
			break
		}
		nameLen := int(raw[i])
		i++
		if i+nameLen > len(raw) {
			break
		}
		name := string(raw[i : i+nameLen])
		i += nameLen
		if i+1 > len(raw) {
			break
		}
		valType := raw[i]
		i++
		if valType != 7 { // only parse UTF-8 string headers
			vlen := 0
			if i+2 <= len(raw) {
				vlen = int(readBE16(raw[i:]))
				i += 2
			}
			if i+vlen <= len(raw) {
				i += vlen
			}
			continue
		}
		if i+2 > len(raw) {
			break
		}
		vlen := int(readBE16(raw[i:]))
		i += 2
		if i+vlen > len(raw) {
			break
		}
		val := string(raw[i : i+vlen])
		i += vlen
		h[name] = val
	}
	return h
}

func readBE16(b []byte) uint16 {
	return uint16(b[0])<<8 | uint16(b[1])
}
