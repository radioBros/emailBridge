package limenmongo

import (
	"fmt"
	"regexp"

	"github.com/thecodearcher/limen"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// condition translates one Limen condition; t (optional) types the comparison value.
func condition(t *Table, w limen.Where) (bson.M, error) {
	f := fieldName(w.Column)
	v := deref(w.Value)
	if t != nil {
		if c, ok := t.Column(w.Column); ok {
			v = coerce(c.Type, v)
		}
	}
	switch w.Operator {
	case limen.OpEq, "":
		return bson.M{f: bson.M{"$eq": v}}, nil
	case limen.OpNe:
		return bson.M{f: bson.M{"$ne": v}}, nil
	case limen.OpLt:
		return bson.M{f: bson.M{"$lt": v}}, nil
	case limen.OpLte:
		return bson.M{f: bson.M{"$lte": v}}, nil
	case limen.OpGt:
		return bson.M{f: bson.M{"$gt": v}}, nil
	case limen.OpGte:
		return bson.M{f: bson.M{"$gte": v}}, nil
	case limen.OpIn, limen.OpNotIn:
		arr, ok := toArray(w.Value)
		if t != nil {
			if c, found := t.Column(w.Column); found {
				for i := range arr {
					arr[i] = coerce(c.Type, arr[i])
				}
			}
		}
		if w.Operator == limen.OpIn {
			if !ok {
				arr = bson.A{} // not a list: matches nothing (Limen's reference adapter)
			}
			return bson.M{f: bson.M{"$in": arr}}, nil
		}
		if !ok {
			return bson.M{}, nil // not a list: excludes nothing
		}
		return bson.M{f: bson.M{"$nin": arr}}, nil
	case limen.OpContains:
		return bson.M{f: bson.M{"$regex": regexp.QuoteMeta(fmt.Sprint(v))}}, nil
	case limen.OpStartsWith:
		return bson.M{f: bson.M{"$regex": "^" + regexp.QuoteMeta(fmt.Sprint(v))}}, nil
	case limen.OpEndsWith:
		return bson.M{f: bson.M{"$regex": regexp.QuoteMeta(fmt.Sprint(v)) + "$"}}, nil
	case limen.OpIsNull:
		return bson.M{f: bson.M{"$eq": nil}}, nil
	case limen.OpIsNotNull:
		return bson.M{f: bson.M{"$ne": nil}}, nil
	}
	return nil, fmt.Errorf("limenmongo: unsupported operator %q", w.Operator)
}

// buildFilter translates Limen conditions: groups joined by OR connectors become $or, groups are ANDed.
func buildFilter(t *Table, conds []limen.Where) (bson.M, error) {
	if len(conds) == 0 {
		return bson.M{}, nil
	}
	var and bson.A
	for _, g := range limen.GroupConditionsByConnector(conds) {
		if len(g) == 1 {
			m, err := condition(t, g[0])
			if err != nil {
				return nil, err
			}
			and = append(and, m)
			continue
		}
		or := make(bson.A, 0, len(g))
		for _, w := range g {
			m, err := condition(t, w)
			if err != nil {
				return nil, err
			}
			or = append(or, m)
		}
		and = append(and, bson.M{"$or": or})
	}
	if len(and) == 1 {
		if m, ok := and[0].(bson.M); ok {
			return m, nil
		}
	}
	return bson.M{"$and": and}, nil
}

// buildSort translates Limen's ORDER BY.
func buildSort(ob []limen.OrderBy) bson.D {
	var d bson.D
	for _, o := range ob {
		if o.Column == "" {
			continue
		}
		dir := 1
		if o.Direction == limen.OrderByDesc {
			dir = -1
		}
		d = append(d, bson.E{Key: fieldName(o.Column), Value: dir})
	}
	return d
}
