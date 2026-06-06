package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Store discovers sessions and caches their metadata between runs.
type Store struct {
	ProjectsDir    string // ~/.claude/projects
	CachePath      string // ~/.config/claude-mgr/index.json
	CodexStatePath string // ~/.codex/state_5.sqlite
	SQLitePath     string // sqlite3 executable

	cache  cacheFile
	Hits   int // files served from cache on the last Scan
	Misses int // files (re)parsed on the last Scan
}

// NewStore builds a Store using standard paths, honoring CLAUDE_MGR_PROJECTS
// and CLAUDE_MGR_CACHE overrides for testing.
func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	projects := os.Getenv("CLAUDE_MGR_PROJECTS")
	if projects == "" {
		projects = filepath.Join(home, ".claude", "projects")
	}
	cache := os.Getenv("CLAUDE_MGR_CACHE")
	if cache == "" {
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		cache = filepath.Join(cfg, "claude-mgr", "index.json")
	}
	codexState := os.Getenv("CLAUDE_MGR_CODEX_STATE")
	if codexState == "" {
		codexState = filepath.Join(home, ".codex", "state_5.sqlite")
	}
	sqlitePath := os.Getenv("CLAUDE_MGR_SQLITE3")
	if sqlitePath == "" {
		sqlitePath = "sqlite3"
	}
	return &Store{ProjectsDir: projects, CachePath: cache, CodexStatePath: codexState, SQLitePath: sqlitePath}, nil
}

// Scan returns all top-level sessions, newest first. Unchanged files are served
// from cache (one stat); changed/new files are tail-parsed. The cache is
// rewritten to reflect the current on-disk set.
func (s *Store) Scan() ([]SessionMeta, error) {
	if s.cache.Entries == nil {
		s.cache = loadCache(s.CachePath)
	}
	s.Hits, s.Misses = 0, 0

	// Depth-1 glob: ~/.claude/projects/<proj>/<id>.jsonl. Nested subagent
	// transcripts live one level deeper and never match this pattern.
	paths, err := filepath.Glob(filepath.Join(s.ProjectsDir, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}

	next := map[string]cacheEntry{}
	out := make([]SessionMeta, 0, len(paths))
	for _, p := range paths {
		if strings.Contains(p, string(os.PathSeparator)+"subagents"+string(os.PathSeparator)) {
			continue // belt-and-suspenders
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		var m SessionMeta
		if e, ok := s.cache.Entries[p]; ok && e.FileSize == fi.Size() && e.FileMtime == fi.ModTime().UnixNano() {
			m = e.toMeta(p)
			s.Hits++
		} else {
			m = SessionMeta{
				SessionID:  strings.TrimSuffix(filepath.Base(p), ".jsonl"),
				Path:       p,
				ProjectDir: filepath.Base(filepath.Dir(p)),
				App:        AppClaude,
				FileSize:   fi.Size(),
				FileMtime:  fi.ModTime(),
			}
			if err := extractMeta(&m); err != nil {
				continue // unreadable; skip this file but keep going
			}
			s.Misses++
		}
		next[p] = metaToEntry(m)
		out = append(out, m)
	}
	if codex, err := s.scanCodex(); err == nil {
		out = append(out, codex...)
	}

	s.cache.Version = cacheVersion
	s.cache.Entries = next
	_ = saveCache(s.CachePath, s.cache) // best-effort; a cache write failure is non-fatal

	sortByRecency(out)
	return out, nil
}

func sortByRecency(ms []SessionMeta) {
	sort.SliceStable(ms, func(i, j int) bool {
		return ms[i].LastActive.After(ms[j].LastActive)
	})
}

// Group is a set of sessions sharing one project (working directory).
type Group struct {
	Label    string // short project label, e.g. "sensorpush/sensor-esp"
	Cwd      string
	Sessions []SessionMeta
	Newest   time.Time
}

// GroupByProject buckets sessions by their authoritative cwd, with groups and
// members each ordered newest-first.
func GroupByProject(ms []SessionMeta) []Group {
	byCwd := map[string]*Group{}
	for _, m := range ms {
		key := m.Cwd
		if key == "" {
			key = m.ProjectDir
		}
		g := byCwd[key]
		if g == nil {
			g = &Group{Label: m.ProjectLabel(), Cwd: m.Cwd}
			byCwd[key] = g
		}
		g.Sessions = append(g.Sessions, m)
		if m.LastActive.After(g.Newest) {
			g.Newest = m.LastActive
		}
	}
	groups := make([]Group, 0, len(byCwd))
	for _, g := range byCwd {
		sortByRecency(g.Sessions)
		groups = append(groups, *g)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].Newest.After(groups[j].Newest)
	})
	return groups
}
