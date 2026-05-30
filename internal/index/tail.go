package index

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

const (
	tailBytes = 64 * 1024 // window read from the end for recent name/activity
	headBytes = 64 * 1024 // window read from the start when title/cwd missing in tail
)

// rawRecord is the subset of a transcript line we care about. Unknown fields
// are ignored by encoding/json.
type rawRecord struct {
	Type        string      `json:"type"`
	Timestamp   string      `json:"timestamp"`
	SessionID   string      `json:"sessionId"`
	Cwd         string      `json:"cwd"`
	GitBranch   string      `json:"gitBranch"`
	IsSidechain bool        `json:"isSidechain"`
	AiTitle     string      `json:"aiTitle"`
	LastPrompt  string      `json:"lastPrompt"`
	Message     *rawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		InputTokens         int `json:"input_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// extractMeta fills the cheap metadata fields of m by reading the file tail,
// falling back to a bounded head read only when the title or cwd is still
// missing. m.Path/ProjectDir/FileSize/FileMtime must already be set.
func extractMeta(m *SessionMeta) error {
	f, err := os.Open(m.Path)
	if err != nil {
		return err
	}
	defer f.Close()

	size := m.FileSize
	var start int64
	if size > tailBytes {
		start = size - tailBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(f, 128*1024)
	if start > 0 {
		// Drop the partial first line so we only parse complete records.
		if _, err := r.ReadBytes('\n'); err != nil && err != io.EOF {
			return err
		}
	}
	scanTail(r, m)

	// Rare fallbacks: only ~1% of sessions lack a last-prompt, and cwd is
	// almost always in the tail. A single head read covers both when needed.
	if (m.AiTitle == "" && m.LastPrompt == "" && m.FirstUserMsg == "") || m.Cwd == "" {
		scanHead(m)
	}
	m.resolveAutoTitle()
	return nil
}

// scanTail walks complete lines from r, keeping the max timestamp and the
// last-seen title/cwd/branch values.
func scanTail(r *bufio.Reader, m *SessionMeta) {
	sc := newLineScanner(r)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		applyRecord(&rec, m, false)
	}
}

// scanHead reads the start of the file to recover the first real user message
// and the cwd when the tail didn't provide them.
func scanHead(m *SessionMeta) {
	f, err := os.Open(m.Path)
	if err != nil {
		return
	}
	defer f.Close()
	lr := io.LimitReader(f, headBytes)
	sc := newLineScanner(bufio.NewReaderSize(lr, 128*1024))
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		applyRecord(&rec, m, true)
		if m.FirstUserMsg != "" && m.Cwd != "" {
			break
		}
	}
}

func applyRecord(rec *rawRecord, m *SessionMeta, head bool) {
	if rec.IsSidechain {
		return
	}
	if ts := parseTS(rec.Timestamp); !ts.IsZero() && ts.After(m.LastActive) {
		m.LastActive = ts
	}
	if rec.Cwd != "" {
		m.Cwd = rec.Cwd
	}
	if rec.GitBranch != "" {
		m.GitBranch = rec.GitBranch
	}
	switch rec.Type {
	case "assistant":
		if rec.Message != nil && rec.Message.Usage != nil {
			u := rec.Message.Usage
			if n := u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens; n > 0 {
				m.ContextTokens = n // last assistant turn wins
			}
		}
	case "ai-title":
		if rec.AiTitle != "" {
			m.AiTitle = rec.AiTitle // last wins
		}
	case "last-prompt":
		if rec.LastPrompt != "" {
			m.LastPrompt = rec.LastPrompt // last wins
		}
	case "user":
		if head && m.FirstUserMsg == "" && rec.Message != nil {
			if txt := userText(rec.Message.Content); txt != "" {
				m.FirstUserMsg = txt
			}
		}
	}
}

// userText extracts plain text from a user message's content, which is either a
// JSON string or an array of typed blocks. Command/meta wrappers are skipped.
func userText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	// String form.
	var s string
	if json.Unmarshal(content, &s) == nil {
		return cleanUserText(s)
	}
	// Array-of-blocks form.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		return cleanUserText(strings.Join(parts, " "))
	}
	return ""
}

// cleanUserText drops obvious non-prompt content (command echoes, system
// reminders, attachment caveats) that would make a poor display title.
func cleanUserText(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(t, "<command-"),
		strings.HasPrefix(t, "<local-command-"),
		strings.HasPrefix(t, "Caveat:"),
		strings.HasPrefix(t, "<system-reminder"):
		return ""
	}
	return t
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}

// newLineScanner returns a bufio.Scanner sized for long JSONL lines (some
// records embed large tool results).
func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	return sc
}
