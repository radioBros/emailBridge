package limenmongo

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// LimenVersion is the Limen core version the embedded schema file was generated with.
const LimenVersion = "v0.2.3"

// NowSentinel is the default value Limen uses for "current time" columns.
const NowSentinel = "@now()"

//go:embed schemas/limen-v0.2.3.json
var embeddedSchema []byte

// EmbeddedSchemaJSON returns a copy of the embedded discovered-schema file (Limen CLI output for
// EmailBridge's plugin set, see the package documentation).
func EmbeddedSchemaJSON() []byte { return append([]byte(nil), embeddedSchema...) }

// Column types used in Limen's schema file (limen.ColumnType values).
const (
	typeUUID   = "uuid"
	typeString = "string"
	typeText   = "text"
	typeInt    = "int"
	typeInt32  = "int32"
	typeInt64  = "int64"
	typeBool   = "bool"
	typeTime   = "time.Time"
	typeMap    = "map[string]any"
)

// Column is one column of a Limen table as described by the schema file.
type Column struct {
	Name         string `json:"Name"`
	Type         string `json:"Type"`
	IsNullable   bool   `json:"IsNullable"`
	IsPrimaryKey bool   `json:"IsPrimaryKey"`
	DefaultValue string `json:"DefaultValue"`
}

// Index is an index of a Limen table as described by the schema file.
type Index struct {
	Name    string   `json:"Name"`
	Columns []string `json:"Columns"`
	Unique  bool     `json:"Unique"`
}

// Table is one Limen table (one MongoDB collection).
type Table struct {
	TableName string   `json:"TableName"`
	Columns   []Column `json:"Columns"`
	Indexes   []Index  `json:"Indexes"`

	byName map[string]Column
}

// Column returns the column definition and whether the table has it.
func (t *Table) Column(name string) (Column, bool) {
	c, ok := t.byName[name]
	return c, ok
}

// Schema is the parsed discovered-schema file: tables keyed by table name.
type Schema struct {
	tables map[string]*Table
}

type schemaFile struct {
	Schemas map[string]*Table `json:"schemas"`
}

// ParseSchema parses a Limen discovered-schema file (.limen/schemas.json).
func ParseSchema(raw []byte) (*Schema, error) {
	var f schemaFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("limenmongo: parse schema file: %w", err)
	}
	if len(f.Schemas) == 0 {
		return nil, errors.New("limenmongo: schema file has no tables")
	}
	s := &Schema{tables: map[string]*Table{}}
	for key, t := range f.Schemas {
		if t == nil {
			continue
		}
		if t.TableName == "" {
			t.TableName = key
		}
		t.byName = make(map[string]Column, len(t.Columns))
		for _, c := range t.Columns {
			if _, err := parseDefault(c, time.Time{}); err != nil {
				return nil, fmt.Errorf("limenmongo: %s.%s: %w", t.TableName, c.Name, err)
			}
			t.byName[c.Name] = c
		}
		s.tables[t.TableName] = t
	}
	return s, nil
}

// DefaultSchema parses the embedded schema file.
func DefaultSchema() (*Schema, error) { return ParseSchema(embeddedSchema) }

// Table returns the table definition, or nil when the schema does not know the table.
func (s *Schema) Table(name string) *Table {
	if s == nil {
		return nil
	}
	return s.tables[name]
}

// TableNames returns the table names in sorted order.
func (s *Schema) TableNames() []string {
	out := make([]string, 0, len(s.tables))
	for n := range s.tables {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// parseDefault turns a schema default into a typed value; ok=false (nil, nil) when there is no default.
func parseDefault(c Column, now time.Time) (any, error) {
	d := c.DefaultValue
	if d == "" {
		return nil, nil
	}
	if d == NowSentinel {
		return now.UTC(), nil
	}
	switch c.Type {
	case typeBool:
		v, err := strconv.ParseBool(d)
		if err != nil {
			return nil, fmt.Errorf("default %q is not a bool", d)
		}
		return v, nil
	case typeInt, typeInt32:
		v, err := strconv.ParseInt(d, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("default %q is not an int32", d)
		}
		return int32(v), nil
	case typeInt64:
		v, err := strconv.ParseInt(d, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("default %q is not an int64", d)
		}
		return v, nil
	case typeTime:
		v, err := time.Parse(time.RFC3339Nano, d)
		if err != nil {
			return nil, fmt.Errorf("default %q is not a time", d)
		}
		return v.UTC(), nil
	default:
		return d, nil
	}
}
