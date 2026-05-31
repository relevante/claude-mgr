// Package watch turns Claude's session registry directory into a change signal,
// so the dashboard can refresh status the moment a session's state flips instead
// of polling on a timer.
//
// Claude rewrites ~/.claude/sessions/<pid>.json in place on every idle/busy/
// waiting transition (verified: same inode, no temp file). macOS kqueue does NOT
// report in-place modifications through a directory watch — only create/delete/
// rename — so each pid file is watched individually; the directory watch catches
// new and removed sessions and (re)arms the per-file watches. This per-file shape
// is also the portable one: it works the same via inotify (Linux) and
// ReadDirectoryChangesW (Windows) through fsnotify.
//
// File-system notifications can coalesce or, rarely, be missed (sleep/wake,
// watch limits), so callers MUST keep a slow safety-net poll for self-healing.
package watch

import (
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// Registry watches a session-registry directory and emits a coalesced signal on
// Events whenever a pid file is created, removed, or rewritten in place.
type Registry struct {
	w      *fsnotify.Watcher
	dir    string
	events chan struct{}
}

// NewRegistry starts watching dir (e.g. ~/.claude/sessions). The caller should
// fall back to plain polling if this returns an error.
func NewRegistry(dir string) (*Registry, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, err
	}
	r := &Registry{w: w, dir: dir, events: make(chan struct{}, 1)}
	r.armExisting()
	go r.loop()
	return r, nil
}

// Events delivers a signal whenever the registry changed. It is buffered(1), so
// a burst of file events collapses into a single pending wake-up.
func (r *Registry) Events() <-chan struct{} { return r.events }

// Close stops watching and releases the underlying file descriptors.
func (r *Registry) Close() error { return r.w.Close() }

// signal wakes the consumer, coalescing if one is already pending.
func (r *Registry) signal() {
	select {
	case r.events <- struct{}{}:
	default:
	}
}

// armExisting adds a per-file watch for every pid file already present.
func (r *Registry) armExisting() {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			_ = r.w.Add(filepath.Join(r.dir, e.Name()))
		}
	}
}

func (r *Registry) loop() {
	for {
		select {
		case ev, ok := <-r.w.Events:
			if !ok {
				return
			}
			if filepath.Ext(ev.Name) != ".json" {
				continue // ignore lock files, temp churn, etc.
			}
			// A new session: watch its pid file so we see its in-place writes.
			if ev.Op&fsnotify.Create != 0 {
				_ = r.w.Add(ev.Name)
			}
			// A session ended: drop the (now stale) per-file watch. kqueue auto-
			// removes on the closed fd; Remove is best-effort elsewhere.
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				_ = r.w.Remove(ev.Name)
			}
			r.signal()
		case _, ok := <-r.w.Errors:
			if !ok {
				return
			}
			// Drop individual errors; the caller's safety-net poll covers gaps.
		}
	}
}
