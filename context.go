// Package gls implements goroutine-local storage.
package gls

import (
	"sync"
)

var (
	mgrRegistry    = make(map[*ContextManager]bool)
	mgrRegistryMtx sync.RWMutex
)

// Values is simply a map of key types to value types. Used by SetValues to
// set multiple values at once.
type Values map[interface{}]interface{}

// ContextManager is the main entrypoint for interacting with
// Goroutine-local-storage. You can have multiple independent ContextManagers
// at any given time. ContextManagers are usually declared globally for a given
// class of context variables. You should use NewContextManager for
// construction.
type ContextManager struct {
	mtx    sync.Mutex
	values []*ValueRecord
}

type ValueRecord struct {
	val     Values
	deleted bool
}

// NewContextManager returns a brand new ContextManager. It also registers the
// new ContextManager in the ContextManager registry which is used by the Go
// method. ContextManagers are typically defined globally at package scope.
func NewContextManager() *ContextManager {
	mgr := &ContextManager{values: make([]*ValueRecord, 1024)}
	mgrRegistryMtx.Lock()
	defer mgrRegistryMtx.Unlock()
	mgrRegistry[mgr] = true
	return mgr
}

// Unregister removes a ContextManager from the global registry, used by the
// Go method. Only intended for use when you're completely done with a
// ContextManager. Use of Unregister at all is rare.
func (m *ContextManager) Unregister() {
	mgrRegistryMtx.Lock()
	defer mgrRegistryMtx.Unlock()
	delete(mgrRegistry, m)
}

// SetValues takes a collection of values and a function to call for those
// values to be set in. Anything further down the stack will have the set
// values available through GetValue. SetValues will add new values or replace
// existing values of the same key and will not mutate or change values for
// previous stack frames.
// SetValues is slow (makes a copy of all current and new values for the new
// gls-context) in order to reduce the amount of lookups GetValue requires.
func (m *ContextManager) SetValues(new_values Values, context_call func()) {
	if len(new_values) == 0 {
		context_call()
		return
	}

	EnsureGoroutineId(func(gid uint) {
		m.mtx.Lock()
		var nested bool
		if gid >= uint(len(m.values)) {
			// If the goroutine id is larger than the current values slice,
			// we need to grow it to accommodate the new gid.
			new_values_slice := make([]*ValueRecord, len(m.values)*2)
			copy(new_values_slice, m.values)
			m.values = new_values_slice
		} else if m.values[gid] != nil {
			// If the value already exists, we will mutate it.
			nested = true
		} else {
			// Otherwise, we will create a new value record.
			m.values[gid] = &ValueRecord{val: make(Values, len(new_values))}
		}
		state := m.values[gid]
		if state == nil {
			state = &ValueRecord{val: make(Values, len(new_values))}
			m.values[gid] = state
		} else if state.deleted {
			state.val = make(Values, len(new_values))
			state.deleted = false
		} else {
			nested = true
		}
		m.mtx.Unlock()

		if nested {
			mutatedKeys := make([]interface{}, 0, len(new_values))
			mutatedVals := make(Values, len(new_values))
			for key, newVal := range new_values {
				mutatedKeys = append(mutatedKeys, key)
				if oldVal, ok := state.val[key]; ok {
					mutatedVals[key] = oldVal
				}
				state.val[key] = newVal
			}
			defer func() {
				for _, key := range mutatedKeys {
					if val, ok := mutatedVals[key]; ok {
						state.val[key] = val
					} else {
						delete(state.val, key)
					}
				}
			}()
		} else {
			for key, newVal := range new_values {
				state.val[key] = newVal
			}
			defer func() {
				state.deleted = true
				state.val = nil
			}()
		}

		context_call()
	})
}

// GetValue will return a previously set value, provided that the value was set
// by SetValues somewhere higher up the stack. If the value is not found, ok
// will be false.
func (m *ContextManager) GetValue(key interface{}) (
	value interface{}, ok bool) {
	gid, ok := GetGoroutineId()
	if !ok {
		return nil, false
	}

	m.mtx.Lock()
	state := m.values[gid]
	m.mtx.Unlock()

	if state == nil {
		return nil, false
	}
	value, ok = state.val[key]
	return value, ok
}

func (m *ContextManager) getValues() Values {
	gid, ok := GetGoroutineId()
	if !ok {
		return nil
	}
	m.mtx.Lock()
	defer m.mtx.Unlock()
	state := m.values[gid]
	if state == nil || state.deleted {
		return nil
	}
	return state.val
}

// Go preserves ContextManager values and Goroutine-local-storage across new
// goroutine invocations. The Go method makes a copy of all existing values on
// all registered context managers and makes sure they are still set after
// kicking off the provided function in a new goroutine. If you don't use this
// Go method instead of the standard 'go' keyword, you will lose values in
// ContextManagers, as goroutines have brand new stacks.
func Go(cb func()) {
	mgrRegistryMtx.RLock()
	defer mgrRegistryMtx.RUnlock()

	for mgr := range mgrRegistry {
		values := mgr.getValues()
		if len(values) > 0 {
			cb = func(mgr *ContextManager, cb func()) func() {
				return func() { mgr.SetValues(values, cb) }
			}(mgr, cb)
		}
	}

	go cb()
}
