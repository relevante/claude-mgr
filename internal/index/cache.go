package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// cacheEntry is the persisted form of a session's cheap metadata, keyed by
// absolute path. FileMtime is stored as Unix nanoseconds for stable equality.
type cacheEntry struct {
	SessionID     string `json:"sessionId"`
	ProjectDir    string `json:"projectDir"`
	Cwd           string `json:"cwd"`
	GitBranch     string `json:"gitBranch"`
	App           string `json:"app,omitempty"`
	Archived      bool   `json:"archived,omitempty"`
	AiTitle       string `json:"aiTitle,omitempty"`
	LastPrompt    string `json:"lastPrompt,omitempty"`
	FirstUserMsg  string `json:"firstUserMsg,omitempty"`
	LastActive    int64  `json:"lastActive"` // unix nanos
	ContextTokens int    `json:"contextTokens"`
	ContextLimit  int    `json:"contextLimit,omitempty"`
	FileSize      int64  `json:"fileSize"`
	FileMtime     int64  `json:"fileMtime"` // unix nanos
}

type cacheFile struct {
	Version int                   `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

const cacheVersion = 4 // bumped: added ContextLimit

func (e cacheEntry) toMeta(path string) SessionMeta {
	app := e.App
	if app == "" {
		app = AppClaude
	}
	m := SessionMeta{
		SessionID:     e.SessionID,
		Path:          path,
		ProjectDir:    e.ProjectDir,
		Cwd:           e.Cwd,
		GitBranch:     e.GitBranch,
		App:           app,
		Archived:      e.Archived,
		AiTitle:       e.AiTitle,
		LastPrompt:    e.LastPrompt,
		FirstUserMsg:  e.FirstUserMsg,
		ContextTokens: e.ContextTokens,
		ContextLimit:  e.ContextLimit,
		FileSize:      e.FileSize,
		FileMtime:     time.Unix(0, e.FileMtime),
	}
	if e.LastActive != 0 {
		m.LastActive = time.Unix(0, e.LastActive)
	}
	m.resolveAutoTitle()
	return m
}

func metaToEntry(m SessionMeta) cacheEntry {
	var la int64
	if !m.LastActive.IsZero() {
		la = m.LastActive.UnixNano()
	}
	return cacheEntry{
		SessionID:     m.SessionID,
		ProjectDir:    m.ProjectDir,
		Cwd:           m.Cwd,
		GitBranch:     m.GitBranch,
		App:           m.AppName(),
		Archived:      m.Archived,
		AiTitle:       m.AiTitle,
		LastPrompt:    m.LastPrompt,
		FirstUserMsg:  m.FirstUserMsg,
		LastActive:    la,
		ContextTokens: m.ContextTokens,
		ContextLimit:  m.ContextLimit,
		FileSize:      m.FileSize,
		FileMtime:     m.FileMtime.UnixNano(),
	}
}

func loadCache(path string) cacheFile {
	c := cacheFile{Version: cacheVersion, Entries: map[string]cacheEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var loaded cacheFile
	if json.Unmarshal(data, &loaded) != nil || loaded.Version != cacheVersion || loaded.Entries == nil {
		return c // ignore corrupt/old cache; rebuild
	}
	return loaded
}

// saveCache writes the cache atomically (temp file + rename).
func saveCache(path string, c cacheFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
