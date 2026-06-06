package watch

import (
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// File watches one path and emits a coalesced signal on create/write/remove.
type File struct {
	w      *fsnotify.Watcher
	path   string
	events chan struct{}
}

// NewFile starts watching path. If the file does not exist yet, its parent
// directory is watched so a later create still signals.
func NewFile(path string) (*File, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(filepath.Dir(path)); err != nil {
		_ = w.Close()
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		_ = w.Add(path)
	}
	f := &File{w: w, path: path, events: make(chan struct{}, 1)}
	go f.loop()
	return f, nil
}

func (f *File) Events() <-chan struct{} { return f.events }

func (f *File) Close() error { return f.w.Close() }

func (f *File) signal() {
	select {
	case f.events <- struct{}{}:
	default:
	}
}

func (f *File) loop() {
	for {
		select {
		case ev, ok := <-f.w.Events:
			if !ok {
				return
			}
			if ev.Name != f.path {
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				_ = f.w.Add(f.path)
			}
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				_ = f.w.Remove(f.path)
			}
			f.signal()
		case _, ok := <-f.w.Errors:
			if !ok {
				return
			}
		}
	}
}
