package replicaset

// Discoverer is an interface for discovering information about
// replicasets.
type Discoverer interface {
	// Discovery returns replicasets information or an error.
	Discovery(force bool) (Replicasets, error)
}

// discovererImpl is an interface that unconditionally performs discovering.
type discovererImpl interface {
	discoveryImpl() (Replicasets, error)
}

// cachedDiscoverer allows to automatically cache discovery results.
type cachedDiscoverer struct {
	discovererImpl
	cached     bool
	discovered Replicasets
}

// newCachedDiscoverer creates cachedDiscoverer.
func newCachedDiscoverer(impl discovererImpl) cachedDiscoverer {
	return cachedDiscoverer{discovererImpl: impl}
}

// Discovery discovers via underlying type.
// If force is false and there is a cached result, returns it.
func (c *cachedDiscoverer) Discovery(force bool) (Replicasets, error) {
	if !force && c.cached {
		return c.discovered, nil
	}
	c.cached = false
	var err error
	c.discovered, err = c.discoveryImpl()
	if err != nil {
		return c.discovered, err
	}
	c.cached = true
	return c.discovered, nil
}

// Type assertion that cachedDiscoverer satisfy Discoverer.
var _ Discoverer = (*cachedDiscoverer)(nil)
