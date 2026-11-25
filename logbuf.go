package main

import (
	"strings"
	"sync"
)

// logBuffer keeps the debug output of the mounts, newest last.
type logBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newLogBuffer() *logBuffer { return &logBuffer{max: 2000} }

// Add appends one line, dropping the oldest when the buffer is full.
func (b *logBuffer) Add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = append(b.lines[:0], b.lines[len(b.lines)-b.max:]...)
	}
}

// Lines returns a snapshot the view can draw.
func (b *logBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

// String is the whole log, for the clipboard.
func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, "\n")
}

// Clear empties the log.
func (b *logBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = b.lines[:0]
}

// Len is the number of lines kept.
func (b *logBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}
