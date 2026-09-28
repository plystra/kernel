package invocation

import "reflect"

const (
	maximumErrorUnwrapLevels = 64
	maximumErrorNodes        = 1024
)

// walkErrorTree visits ordinary unwrap edges only. A path-local set permits
// repeated shared subtrees but rejects comparable cycles; non-comparable
// custom values still cannot evade the depth and total-node limits.
func walkErrorTree(root error, visit func(error)) (complete bool) {
	defer func() {
		if recover() != nil {
			complete = false
		}
	}()
	path := make(map[error]bool)
	visited := 0
	var walk func(error, int) bool
	walk = func(node error, depth int) bool {
		if node == nil {
			return true
		}
		if depth > maximumErrorUnwrapLevels || visited == maximumErrorNodes {
			return false
		}
		visited++
		if reflect.ValueOf(node).Comparable() {
			if path[node] {
				return false
			}
			path[node] = true
			defer delete(path, node)
		}
		visit(node)
		switch value := node.(type) {
		case interface{ Unwrap() error }:
			return walk(value.Unwrap(), depth+1)
		case interface{ Unwrap() []error }:
			children := value.Unwrap()
			if len(children) > maximumErrorNodes-visited {
				return false
			}
			for _, child := range children {
				if !walk(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	return walk(root, 0)
}
