package cache

var NoCache Cache = &noCache{}

type noCache struct{}

func (m *noCache) GetOrSet(_ any, setFn SetFn) (v any, err error) {
	return setFn()
}

func (m *noCache) GetOrSetWithExpiry(_ any, setFn SetFnWithExpiry) (v any, err error) {
	v, _, err = setFn()
	return v, err
}

// Delete is a no-op: nothing is cached. // Modified by KAOS (repository cache invalidation, kaos.28).
func (m *noCache) Delete(_ any) {}
