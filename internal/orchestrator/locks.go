package orchestrator

import "sync"

type operationLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (l *operationLocks) lock(key string) func() {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*sync.Mutex)
	}
	mutex := l.locks[key]
	if mutex == nil {
		mutex = &sync.Mutex{}
		l.locks[key] = mutex
	}
	l.mu.Unlock()
	mutex.Lock()
	return mutex.Unlock
}
