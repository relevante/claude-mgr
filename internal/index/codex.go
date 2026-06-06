package index

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const codexThreadsQuery = `
select
  id,
  rollout_path,
  cwd,
  title,
  first_user_message,
  preview,
  tokens_used,
  created_at_ms,
  updated_at_ms,
  git_branch,
  archived,
  source,
  coalesce(thread_source, '') as thread_source
from threads
where source = 'cli'
  and coalesce(thread_source, '') in ('', 'user')
order by updated_at_ms desc, id desc;`

type codexThreadRow struct {
	ID               string `json:"id"`
	RolloutPath      string `json:"rollout_path"`
	Cwd              string `json:"cwd"`
	Title            string `json:"title"`
	FirstUserMessage string `json:"first_user_message"`
	Preview          string `json:"preview"`
	TokensUsed       int    `json:"tokens_used"`
	CreatedAtMS      int64  `json:"created_at_ms"`
	UpdatedAtMS      int64  `json:"updated_at_ms"`
	GitBranch        string `json:"git_branch"`
	Archived         int    `json:"archived"`
	Source           string `json:"source"`
	ThreadSource     string `json:"thread_source"`
}

type codexRolloutRecord struct {
	Type    string `json:"type"`
	Payload *struct {
		Type string `json:"type"`
		Info *struct {
			LastTokenUsage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

func (s *Store) scanCodex() ([]SessionMeta, error) {
	if s.CodexStatePath == "" {
		return nil, nil
	}
	if fi, err := os.Stat(s.CodexStatePath); err != nil || fi.IsDir() {
		return nil, nil
	}
	sqlitePath := s.SQLitePath
	if sqlitePath == "" {
		sqlitePath = "sqlite3"
	}
	cmd := exec.Command(sqlitePath, "-readonly", "-json", "-cmd", ".timeout 1000", s.CodexStatePath, codexThreadsQuery)
	data, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	var rows []codexThreadRow
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := codexRowsToMeta(rows)
	fillCodexFileInfo(out)
	return out, nil
}

func (r codexThreadRow) interactive() bool {
	if strings.TrimSpace(r.ID) == "" {
		return false
	}
	if strings.TrimSpace(r.Source) != "cli" {
		return false
	}
	switch strings.TrimSpace(r.ThreadSource) {
	case "", "user":
		return true
	default:
		return false
	}
}

func codexRowsToMeta(rows []codexThreadRow) []SessionMeta {
	out := make([]SessionMeta, 0, len(rows))
	for _, r := range rows {
		if !r.interactive() {
			continue
		}
		last := millisTime(r.UpdatedAtMS)
		if last.IsZero() {
			last = millisTime(r.CreatedAtMS)
		}
		m := SessionMeta{
			SessionID:    r.ID,
			Path:         r.RolloutPath,
			ProjectDir:   codexProjectDir(r.Cwd),
			Cwd:          r.Cwd,
			GitBranch:    r.GitBranch,
			LastActive:   last,
			AiTitle:      r.Title,
			LastPrompt:   r.Preview,
			FirstUserMsg: r.FirstUserMessage,
			App:          AppCodex,
			Archived:     r.Archived != 0,
		}
		m.resolveAutoTitle()
		out = append(out, m)
	}
	return out
}

func fillCodexFileInfo(ms []SessionMeta) {
	for i := range ms {
		if ms[i].Path == "" {
			continue
		}
		fi, err := os.Stat(ms[i].Path)
		if err != nil || fi.IsDir() {
			continue
		}
		ms[i].FileSize = fi.Size()
		ms[i].FileMtime = fi.ModTime()
		extractCodexUsage(&ms[i])
	}
}

func extractCodexUsage(m *SessionMeta) {
	f, err := os.Open(m.Path)
	if err != nil {
		return
	}
	defer f.Close()

	size := m.FileSize
	var start int64
	if size > tailBytes {
		start = size - tailBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 128*1024)
	if start > 0 {
		if _, err := r.ReadBytes('\n'); err != nil && err != io.EOF {
			return
		}
	}
	sc := newLineScanner(r)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec codexRolloutRecord
		if json.Unmarshal(line, &rec) != nil || rec.Type != "event_msg" ||
			rec.Payload == nil || rec.Payload.Type != "token_count" || rec.Payload.Info == nil {
			continue
		}
		info := rec.Payload.Info
		if info.LastTokenUsage != nil && info.LastTokenUsage.TotalTokens > 0 {
			m.ContextTokens = info.LastTokenUsage.TotalTokens
		}
		if info.ModelContextWindow > 0 {
			m.ContextLimit = info.ModelContextWindow
		}
	}
}

func codexProjectDir(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(cwd))
}

func millisTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
