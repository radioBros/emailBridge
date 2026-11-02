package rules

import (
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

func mergeFn(a, b ref.Val) ref.Val {
	am := asStringMap(a)
	bm := asStringMap(b)
	out := make(map[string]any, len(am)+len(bm))
	for k, v := range am {
		out[k] = v
	}
	for k, v := range bm {
		out[k] = v
	}
	return types.DefaultTypeAdapter.NativeToValue(out)
}

func pickFn(m, keys ref.Val) ref.Val {
	src := asStringMap(m)
	out := map[string]any{}
	for _, k := range stringKeys(keys) {
		if v, ok := src[k]; ok {
			out[k] = v
		}
	}
	return types.DefaultTypeAdapter.NativeToValue(out)
}

func omitFn(m, keys ref.Val) ref.Val {
	src := asStringMap(m)
	drop := map[string]struct{}{}
	for _, k := range stringKeys(keys) {
		drop[k] = struct{}{}
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		if _, skip := drop[k]; !skip {
			out[k] = v
		}
	}
	return types.DefaultTypeAdapter.NativeToValue(out)
}

func asStringMap(v ref.Val) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	switch x := v.Value().(type) {
	case map[string]any:
		return x
	}
	if mapper, ok := v.(traits.Mapper); ok {
		out := map[string]any{}
		it := mapper.Iterator()
		for it.HasNext() == types.True {
			k := it.Next()
			ks, ok := k.(types.String)
			if !ok {
				continue
			}
			out[string(ks)] = mapper.Get(k).Value()
		}
		return out
	}
	return map[string]any{}
}

func stringKeys(v ref.Val) []string {
	lister, ok := v.(traits.Lister)
	if !ok {
		return nil
	}
	n := int(lister.Size().(types.Int))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		item := lister.Get(types.Int(i))
		if s, ok := item.(types.String); ok {
			out = append(out, string(s))
		}
	}
	return out
}
