package agentsvc

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// FileLogSink is the default LogSink: it appends each chunk to
// <root>/builds/<job>/<n>/log.live under an exclusive flock so
// concurrent writers serialize cleanly. The on-disk file is
// what the Phase 6 SSE log-tail endpoint follows.
type FileLogSink struct {
	root string
}

// NewFileLogSink constructs a FileLogSink rooted at the same
// directory as the server's storage.Storage.
func NewFileLogSink(root string) *FileLogSink {
	return &FileLogSink{root: root}
}

// LiveLogPath returns the on-disk path FileLogSink writes to
// for the given (jobName, buildNum). Useful for tests and
// readers that want to know the file location.
func (s *FileLogSink) LiveLogPath(jobName string, buildNum int) string {
	return filepath.Join(s.root, "builds", jobName, strconv.Itoa(buildNum), "log.live")
}

// WriteLogChunk implements LogSink. It silently drops chunks
// whose jobName is empty (i.e. no in-flight SubmitAndWait knows
// what job the build_number belongs to).
func (s *FileLogSink) WriteLogChunk(jobName string, buildNum int, data []byte) {
	if jobName == "" {
		return
	}
	path := s.LiveLogPath(jobName, buildNum)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "agentsvc.FileLogSink: mkdir %s: %v\n", filepath.Dir(path), err)
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentsvc.FileLogSink: open %s: %v\n", path, err)
		return
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		fmt.Fprintf(os.Stderr, "agentsvc.FileLogSink: flock %s: %v\n", path, err)
		return
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	if _, err := f.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "agentsvc.FileLogSink: write %s: %v\n", path, err)
	}
}
