//go:build unix

package session

import (
	"fmt"
	"os"
	"syscall"
)

// lock takes an exclusive advisory lock for one session id, held until the
// returned release runs.
//
// The lock lives in its own file rather than on the record, because the record
// is replaced by rename: a lock held on that inode would guard a file no one
// is reading any more the moment another process swapped it underneath.
func (s *Store) lock(id string) (func(), error) {
	f, err := os.OpenFile(s.lockPath(id), os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		return nil, fmt.Errorf("opening lock for %s: %w", id, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking %s: %w", id, err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
