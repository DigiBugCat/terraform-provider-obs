package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// FilterSpec exposes typed settings while retaining the exact same native lifecycle.
// Settings names are OBS top-level keys. No implicit button/action execution occurs.
type FilterSpec struct {
	TypeSuffix, Kind string
	Settings         map[string]FilterSettingType
	Attributes       map[string]schema.Attribute
	EncodeSettings   func(map[string]any) (map[string]any, error)
	DecodeSettings   func(map[string]any) (map[string]any, error)
	ValidateNative   func(context.Context, func(context.Context, string, map[string]any) (map[string]any, error), string, string, map[string]any) error
	// ImportID, when set, parses a resource-specific import ID before the generic JSON
	// form is tried, so a typed resource can keep an older import ID shape.
	ImportID func(id string) (collection, parentUUID, parentName, name string, ok bool)
}

func (r *genericFilterResource) validateTypedNative(ctx context.Context, cl requester, m genericFilterModel) error {
	if r.spec == nil || r.spec.ValidateNative == nil {
		return nil
	}
	return r.spec.ValidateNative(ctx, cl.Req, m.ParentName.ValueString(), m.ParentUUID.ValueString(), mustGenericSettings(m.SettingsJSON.ValueString()))
}

type FilterSettingType string

const (
	FilterString  FilterSettingType = "string"
	FilterBool    FilterSettingType = "bool"
	FilterFloat64 FilterSettingType = "float64"
	FilterInt64   FilterSettingType = "int64"
)

func NewTypedFilterResource(spec FilterSpec) resource.Resource {
	return &genericFilterResource{spec: &spec}
}
func (r *genericFilterResource) typedSchema(s *schema.Schema) {
	if r.spec == nil {
		return
	}
	delete(s.Attributes, "settings_json")
	s.Attributes["kind"] = schema.StringAttribute{Computed: true}
	for k, t := range r.spec.Settings {
		switch t {
		case FilterString:
			s.Attributes[k] = schema.StringAttribute{Required: true, Sensitive: true}
		case FilterBool:
			s.Attributes[k] = schema.BoolAttribute{Required: true}
		case FilterFloat64:
			s.Attributes[k] = schema.Float64Attribute{Required: true}
		case FilterInt64:
			s.Attributes[k] = schema.Int64Attribute{Required: true}
		default:
			panic("unsupported FilterSpec type")
		}
	}
	for k, v := range r.spec.Attributes {
		s.Attributes[k] = v
	}
}

type genericModelReader interface {
	Get(context.Context, any) diag.Diagnostics
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}
type genericModelWriter interface {
	Set(context.Context, any) diag.Diagnostics
	SetAttribute(context.Context, path.Path, any) diag.Diagnostics
}

func (r *genericFilterResource) readModel(ctx context.Context, src genericModelReader, m *genericFilterModel) diag.Diagnostics {
	if r.spec == nil {
		return src.Get(ctx, m)
	}
	var ds diag.Diagnostics
	for k, v := range map[string]any{"id": &m.ID, "collection": &m.Collection, "parent_name": &m.ParentName, "parent_uuid": &m.ParentUUID, "name": &m.Name, "enabled": &m.Enabled, "index": &m.Index} {
		ds.Append(src.GetAttribute(ctx, path.Root(k), v)...)
	}
	m.Kind = types.StringValue(r.spec.Kind)
	settings := map[string]any{}
	for k, t := range r.spec.Settings {
		switch t {
		case FilterString:
			var v types.String
			ds.Append(src.GetAttribute(ctx, path.Root(k), &v)...)
			settings[k] = v.ValueString()
		case FilterBool:
			var v types.Bool
			ds.Append(src.GetAttribute(ctx, path.Root(k), &v)...)
			settings[k] = v.ValueBool()
		case FilterFloat64:
			var v types.Float64
			ds.Append(src.GetAttribute(ctx, path.Root(k), &v)...)
			settings[k] = v.ValueFloat64()
		case FilterInt64:
			var v types.Int64
			ds.Append(src.GetAttribute(ctx, path.Root(k), &v)...)
			settings[k] = v.ValueInt64()
		}
	}
	if r.spec.EncodeSettings != nil {
		var err error
		settings, err = r.spec.EncodeSettings(settings)
		if err != nil {
			ds.AddError("Invalid typed settings", err.Error())
			return ds
		}
	}
	b, e := json.Marshal(settings)
	if e != nil {
		ds.AddError("Invalid typed settings", e.Error())
	}
	m.SettingsJSON = types.StringValue(string(b))
	return ds
}
func (r *genericFilterResource) writeModel(ctx context.Context, dst genericModelWriter, m *genericFilterModel) diag.Diagnostics {
	if r.spec == nil {
		return dst.Set(ctx, m)
	}
	var ds diag.Diagnostics
	for k, v := range map[string]any{"id": m.ID, "collection": m.Collection, "parent_name": m.ParentName, "parent_uuid": m.ParentUUID, "name": m.Name, "kind": m.Kind, "enabled": m.Enabled, "index": m.Index} {
		ds.Append(dst.SetAttribute(ctx, path.Root(k), v)...)
	}
	settings, e := parseGenericSettings(m.SettingsJSON.ValueString())
	if e != nil {
		ds.AddError("Invalid typed settings", e.Error())
		return ds
	}
	if r.spec.DecodeSettings != nil {
		settings, e = r.spec.DecodeSettings(settings)
		if e != nil {
			ds.AddError("Invalid typed settings", e.Error())
			return ds
		}
	}
	for k, t := range r.spec.Settings {
		v := settings[k]
		var out any
		switch t {
		case FilterString:
			s, ok := v.(string)
			if !ok {
				ds.AddError("Invalid typed setting", fmt.Sprintf("%s requires string", k))
				continue
			}
			out = types.StringValue(s)
		case FilterBool:
			b, ok := v.(bool)
			if !ok {
				ds.AddError("Invalid typed setting", fmt.Sprintf("%s requires bool", k))
				continue
			}
			out = types.BoolValue(b)
		case FilterFloat64, FilterInt64:
			n, ok := numberValue(v)
			if !ok {
				ds.AddError("Invalid typed setting", fmt.Sprintf("%s requires number", k))
				continue
			}
			if t == FilterInt64 {
				if n != float64(int64(n)) {
					ds.AddError("Invalid typed setting", fmt.Sprintf("%s requires integer", k))
					continue
				}
				out = types.Int64Value(int64(n))
			} else {
				out = types.Float64Value(n)
			}
		}
		ds.Append(dst.SetAttribute(ctx, path.Root(k), out)...)
	}
	return ds
}
