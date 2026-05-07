package filesystem

import (
	"fmt"
	"sync"
)

// Manager is a registry of named Disks with a designated default.
// Useful for applications that store files across multiple backends —
// uploads on local disk, public assets on a CDN-fronted bucket, etc.
//
// The Manager is not a Disk itself — call Disk(name) or Default() to
// get a Disk for actual operations. This keeps the Disk interface
// stable (one source of truth) and avoids duplicating its method set
// on the Manager.
type Manager struct {
	mu          sync.RWMutex
	disks       map[string]Disk
	defaultName string
}

// NewManager constructs a Manager with the given default disk
// registered under defaultName. Panics if disk is nil — better to
// catch the wiring bug at startup than during requests.
func NewManager(defaultName string, disk Disk) *Manager {
	if disk == nil {
		panic("filesystem: NewManager requires a non-nil default disk")
	}
	if defaultName == "" {
		panic("filesystem: NewManager requires a defaultName")
	}
	m := &Manager{
		disks:       make(map[string]Disk),
		defaultName: defaultName,
	}
	m.disks[defaultName] = disk
	return m
}

// Register adds a named disk. Re-registering an existing name replaces
// it — useful in tests for swapping in a memory disk over a real one.
func (m *Manager) Register(name string, disk Disk) {
	if disk == nil {
		panic(fmt.Sprintf("filesystem: cannot register nil disk under %q", name))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disks[name] = disk
}

// Disk returns the disk registered under name. Panics if no such disk
// exists — programming/wiring error, fail loud.
func (m *Manager) Disk(name string) Disk {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.disks[name]
	if !ok {
		panic(fmt.Sprintf("filesystem: no disk registered under %q", name))
	}
	return d
}

// Default returns the default disk. Identical to Disk(m.DefaultName()).
func (m *Manager) Default() Disk {
	return m.Disk(m.defaultName)
}

// DefaultName returns the name under which the default disk is registered.
func (m *Manager) DefaultName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultName
}

// SetDefault changes which registered disk is the default. Panics if
// name isn't registered.
func (m *Manager) SetDefault(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.disks[name]; !ok {
		panic(fmt.Sprintf("filesystem: cannot set default to unregistered disk %q", name))
	}
	m.defaultName = name
}

// Names returns all registered disk names. Useful for diagnostic
// endpoints and tests.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.disks))
	for n := range m.disks {
		out = append(out, n)
	}
	return out
}
