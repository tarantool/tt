package formatter

type unorderedMap[T comparable] struct {
	innerMap map[T]any
	keys     []T
}

func createUnorderedMap[T comparable](length int) unorderedMap[T] {
	return unorderedMap[T]{
		innerMap: make(map[T]any),
		keys:     make([]T, 0, length),
	}
}

func (u *unorderedMap[T]) insert(key T, value any) {
	u.keys = append(u.keys, key)
	u.innerMap[key] = value
}

func (u *unorderedMap[T]) forEach(f func(key T, value any)) {
	for _, key := range u.keys {
		f(key, u.innerMap[key])
	}
}
