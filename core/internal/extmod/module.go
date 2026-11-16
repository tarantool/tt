package extmod

import (
	"fmt"
	"sync"

	"github.com/spf13/cobra"
)

// Module is an external module tt found: a command of tt by its name. What
// the module is - its executable, its version and its one-line help - is
// learnt only when something needs it: its run reads its manifest, and a
// listing of the commands asks it to describe itself (Describe).
type Module struct {
	// Name is the name of the module, the command it is run as.
	Name string

	entry modulesEntry
	opts  *Options
	// proxy is the command that runs the module, nil when -I keeps the
	// command of its name in its place.
	proxy *cobra.Command

	once     sync.Once
	manifest Manifest
	err      error

	warnOnce sync.Once
}

// newModule returns the module name found at entry.
func newModule(name string, entry modulesEntry, opts *Options) *Module {
	var none Manifest

	return &Module{
		Name: name, entry: entry, opts: opts, proxy: nil,
		once: sync.Once{}, manifest: none, err: nil, warnOnce: sync.Once{},
	}
}

// Describe returns the manifest of the module, learnt once: the one its
// manifest file holds, or the one its executable answers --description
// --version with, from the cache when the executable has not changed since
// it answered. Nothing is warned about; the error says why the module
// cannot be described.
func (m *Module) Describe() (Manifest, error) {
	m.once.Do(func() {
		m.manifest, m.err = makeManifest(m.entry, m.opts.Open, m.opts.CacheDir)
		m.manifest.Name = m.Name

		if m.err == nil && m.proxy != nil {
			// Listed and completed by the help it gives, as any command is
			// by its description.
			m.proxy.Short = m.manifest.Help
		}
	})

	return m.manifest, m.err
}

// Listed returns the manifest of the module for a listing of the commands,
// such as the help of tt: Describe, warning once, through the log, when the
// module cannot be described - its executable fails or cannot be run, its
// manifest cannot be read. A module that cannot be described is listed with
// its name alone.
func (m *Module) Listed() Manifest {
	manifest, err := m.Describe()
	if err != nil {
		m.warnOnce.Do(func() {
			warnf(m.opts.Log, "Failed to get information about module %q: %s", m.Name, err)
		})

		return Manifest{
			Name: m.Name, Version: "", Main: m.entry.Main, Help: "",
			TtVersion: "", Description: "", Homepage: "",
		}
	}

	return manifest
}

// Help returns the one-line help of the module for a listing (Listed).
func (m *Module) Help() string {
	return m.Listed().Help
}

// executable returns the executable the module runs: the one its manifest
// names, or its main. Nothing is run to find it.
func (m *Module) executable() (string, error) {
	if m.entry.Manifest == "" {
		return m.entry.Main, nil
	}

	manifest, err := m.Describe()
	if err != nil {
		return "", fmt.Errorf("external module %q: %w", m.Name, err)
	}

	return manifest.Main, nil
}

// location names the module in messages: its main, or its directory when
// only its manifest names the executable.
func (m *Module) location() string {
	if m.entry.Main != "" {
		return m.entry.Main
	}

	return m.entry.Directory
}
