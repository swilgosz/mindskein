//go:build !unix

package session

// lock is a no-op where advisory locking is unavailable. A hook must keep
// working regardless of platform, and losing the race costs one status update.
func (s *Store) lock(string) (func(), error) { return func() {}, nil }
