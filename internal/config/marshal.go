package config

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var durationType = reflect.TypeOf(time.Duration(0))

// MarshalYAML serializes cfg using the mapstructure tag names
// (snake_case), matching exactly how the configuration is loaded
// back via Viper. A plain yaml.Marshal would emit Go field names
// (camelCase), producing files whose keys do not round-trip
// (e.g. mcp_port/context_window).
//
// time.Duration values are emitted in their string form ("120s")
// so Viper's string→duration decode hook can parse them back.
// Nil slices/maps are emitted as YAML null, which loads back as nil.
func MarshalYAML(cfg *WukongConfig) ([]byte, error) {
	root, err := mapstructureKeyed(reflect.ValueOf(cfg))
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(root)
}

// mapstructureKeyed recursively converts v into plain Go values
// whose map keys are the mapstructure tag names of the struct
// fields, dropping unexported/untagged fields.
func mapstructureKeyed(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil, nil
		}
		return mapstructureKeyed(v.Elem())
	case reflect.Struct:
		// time.Duration is an int64 kind; handle before the numeric
		// branches so it serializes as "120s" instead of nanoseconds.
		if v.Type() == durationType {
			return v.Interface().(time.Duration).String(), nil
		}
		m := make(map[string]any)
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("mapstructure")
			if tag == "" || tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			val, err := mapstructureKeyed(v.Field(i))
			if err != nil {
				return nil, err
			}
			m[name] = val
		}
		return m, nil
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil, nil
		}
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			val, err := mapstructureKeyed(v.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = val
		}
		return out, nil
	case reflect.Map:
		if v.IsNil() {
			return nil, nil
		}
		out := make(map[string]any, v.Len())
		it := v.MapRange()
		for it.Next() {
			key := fmt.Sprint(it.Key().Interface())
			val, err := mapstructureKeyed(it.Value())
			if err != nil {
				return nil, err
			}
			out[key] = val
		}
		return out, nil
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		if v.Type() == durationType {
			return v.Interface().(time.Duration).String(), nil
		}
		return v.Interface(), nil
	default:
		return nil, fmt.Errorf("config: unsupported kind %s for field %s", v.Kind(), v.Type())
	}
}
