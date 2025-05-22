package formatter

// nodeType defines a set of supported render node types.
type nodeType int

const (
	scalarNodeType nodeType = iota
	arrayNodeType
	mapNodeType
)

// getNodeType returns renderNode type.
func getNodeType(node any) nodeType {
	switch node.(type) {
	case unorderedMap[any]:
		return mapNodeType
	case map[any]any:
		return mapNodeType
	case []any:
		return arrayNodeType
	default:
		return scalarNodeType
	}
}

// isNodeTypeEqual checks renderNode equal.
func isNodeTypeEqual(x, y any) bool {
	return getNodeType(x) == getNodeType(y)
}
