package replicaset

// CacheBehavior describes a cache behavior.
type CacheBehavior bool

const (
	UseCache  CacheBehavior = false
	SkipCache               = true
)

// Discoverer is an interface for discovering information about
// replicasets.
type Discoverer interface {
	// Discovery returns replicasets information or an error.
	Discovery(behavior CacheBehavior) (Replicasets, error)
}

// discoverer is an interface that unconditionally performs discovering.
type discoverer interface {
	discovery() (Replicasets, error)
}

// cachedDiscoverer allows to automatically cache discovery results.
type cachedDiscoverer struct {
	discoverer
	cached      bool
	replicasets Replicasets
}

// Discovery discovers via underlying type.
// If behavior is UseCache and there is a cached result, returns it.
func (c *cachedDiscoverer) Discovery(behavior CacheBehavior) (Replicasets, error) {
	if behavior == UseCache && c.cached {
		return c.replicasets, nil
	}
	c.cached = false
	var err error
	c.replicasets, err = c.discovery()
	if err != nil {
		return c.replicasets, err
	}
	c.cached = true
	return c.replicasets, nil
}

// Type assertion that cachedDiscoverer satisfy Discoverer.
var _ Discoverer = (*cachedDiscoverer)(nil)
