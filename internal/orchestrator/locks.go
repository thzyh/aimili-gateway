package orchestrator

import (
	"context"
	"sync"
)

type mutationContextKey struct{}

type operationLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (o *Orchestrator) lockMutation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(mutationContextKey{}) != nil {
		return ctx, func() {}
	}
	unlock := o.locks.lock("mutation")
	return context.WithValue(ctx, mutationContextKey{}, struct{}{}), unlock
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
