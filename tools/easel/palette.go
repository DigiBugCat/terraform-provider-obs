package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/DigiBugCat/terraform-provider-obs/tfprovider"
)

// The palette is everything the easel may paint with that it did not get from the
// painter: the provider's schema, which resources can be imported, and the text of the
// source tree. A brush that names code (an attribute, a kind string, an import form)
// may only use what is on the palette.

type attr struct {
	Path      string // dotted, e.g. "transform.x"
	Name      string
	Type      string
	Desc      string
	Required  bool
	Optional  bool
	Computed  bool
	Sensitive bool
	Nesting   string  // "", "single", "list", "set", "map"
	Children  []*attr // nested attributes
}

type schemaOf struct {
	Type       string // "obs_scene", or "provider"
	Desc       string
	Attrs      []*attr
	Importable bool
}

type palette struct {
	Provider  *schemaOf
	Resources map[string]*schemaOf
	corpus    string
}

func typeName(t tftypes.Type) string {
	switch {
	case t == nil:
		return "Dynamic"
	case t.Is(tftypes.String):
		return "String"
	case t.Is(tftypes.Number):
		return "Number"
	case t.Is(tftypes.Bool):
		return "Boolean"
	case t.Is(tftypes.List{}):
		return "List of " + typeName(t.(tftypes.List).ElementType)
	case t.Is(tftypes.Set{}):
		return "Set of " + typeName(t.(tftypes.Set).ElementType)
	case t.Is(tftypes.Map{}):
		return "Map of " + typeName(t.(tftypes.Map).ElementType)
	case t.Is(tftypes.Object{}):
		return "Object"
	}
	return "Dynamic"
}

func convert(prefix string, in []*tfprotov6.SchemaAttribute) []*attr {
	var out []*attr
	for _, a := range in {
		x := &attr{Name: a.Name, Path: prefix + a.Name, Desc: strings.TrimSpace(a.Description),
			Required: a.Required, Optional: a.Optional, Computed: a.Computed, Sensitive: a.Sensitive}
		if a.NestedType != nil {
			switch a.NestedType.Nesting {
			case tfprotov6.SchemaObjectNestingModeList:
				x.Nesting, x.Type = "list", "Attributes List"
			case tfprotov6.SchemaObjectNestingModeSet:
				x.Nesting, x.Type = "set", "Attributes Set"
			case tfprotov6.SchemaObjectNestingModeMap:
				x.Nesting, x.Type = "map", "Attributes Map"
			default:
				x.Nesting, x.Type = "single", "Attributes"
			}
			x.Children = convert(x.Path+".", a.NestedType.Attributes)
		} else {
			x.Type = typeName(a.Type)
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func loadPalette(root string) (*palette, error) {
	ctx := context.Background()
	prov := tfprovider.New()
	srv := providerserver.NewProtocol6(prov)()
	resp, err := srv.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		return nil, err
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			return nil, fmt.Errorf("provider schema: %s: %s", d.Summary, d.Detail)
		}
	}
	p := &palette{Resources: map[string]*schemaOf{}}
	p.Provider = &schemaOf{Type: "provider", Desc: strings.TrimSpace(resp.Provider.Block.Description), Attrs: convert("", resp.Provider.Block.Attributes)}
	for name, s := range resp.ResourceSchemas {
		p.Resources[name] = &schemaOf{Type: name, Desc: strings.TrimSpace(s.Block.Description), Attrs: convert("", s.Block.Attributes)}
	}
	for _, mk := range prov.Resources(ctx) {
		r := mk()
		var md resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "obs"}, &md)
		if _, ok := r.(resource.ResourceWithImportState); ok {
			if s := p.Resources[md.TypeName]; s != nil {
				s.Importable = true
			}
		}
	}
	p.corpus = readCorpus(root)
	return p, nil
}

// corpusRoots is the source a backticked name may be quoted from. It is the part of the
// tree that ships with the provider, so a name that verifies here exists for a reader.
var corpusRoots = []string{"main.go", "tfprovider", "internal/ws", "internal/obsplugins", "internal/absoluteshell",
	"native-bridge", "examples/basic", "examples/provider", "examples/resources", "examples/guides", "cmd/obs-inventory",
	"cmd/obs-add-item", "cmd/obs-purge-prefix", "GNUmakefile", ".goreleaser.yml", "terraform-registry-manifest.json", "go.mod"}

func readCorpus(root string) string {
	var b strings.Builder
	keep := map[string]bool{".go": true, ".tf": true, ".json": true, ".yml": true, ".yaml": true, ".sh": true, ".c": true,
		".cpp": true, ".h": true, ".mm": true, ".m": true, ".mod": true, "": true}
	for _, r := range corpusRoots {
		filepath.Walk(filepath.Join(root, r), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if n := info.Name(); n == "build" || n == ".terraform" {
					return filepath.SkipDir
				}
				return nil
			}
			if !keep[filepath.Ext(path)] || info.Size() > 2<<20 {
				return nil
			}
			if raw, err := os.ReadFile(path); err == nil {
				b.Write(raw)
				b.WriteByte('\n')
			}
			return nil
		})
	}
	return b.String()
}

// find resolves a dotted attribute path in a schema.
func (s *schemaOf) find(path string) *attr {
	list := s.Attrs
	var hit *attr
	for _, part := range strings.Split(path, ".") {
		hit = nil
		for _, a := range list {
			if a.Name == part {
				hit = a
				break
			}
		}
		if hit == nil {
			return nil
		}
		list = hit.Children
	}
	return hit
}

func (s *schemaOf) walk(fn func(*attr)) {
	var rec func([]*attr)
	rec = func(as []*attr) {
		for _, a := range as {
			fn(a)
			rec(a.Children)
		}
	}
	rec(s.Attrs)
}

// attrNames is every attribute name any schema declares, at any depth.
func (p *palette) attrNames() map[string]bool {
	out := map[string]bool{}
	add := func(a *attr) { out[a.Name] = true }
	p.Provider.walk(add)
	for _, s := range p.Resources {
		s.walk(add)
	}
	return out
}

func (p *palette) resourceNames() []string {
	var out []string
	for n := range p.Resources {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// schemaFor maps a page ("index", "resources/scene") to the schema it documents.
func (p *palette) schemaFor(page string) *schemaOf {
	if page == "index" {
		return p.Provider
	}
	if rest, ok := strings.CutPrefix(page, "resources/"); ok {
		return p.Resources["obs_"+rest]
	}
	return nil
}

func (p *palette) print(filter string) {
	show := func(s *schemaOf) {
		imp := ""
		if s.Importable {
			imp = "  [importable]"
		}
		fmt.Printf("%s%s\n", s.Type, imp)
		if s.Desc != "" {
			fmt.Printf("    : %s\n", s.Desc)
		}
		s.walk(func(a *attr) {
			mode := "read-only"
			switch {
			case a.Required:
				mode = "required"
			case a.Optional && a.Computed:
				mode = "optional+computed"
			case a.Optional:
				mode = "optional"
			}
			sens := ""
			if a.Sensitive {
				sens = ", sensitive"
			}
			desc := a.Desc
			if desc == "" {
				desc = "(bare)"
			}
			fmt.Printf("  %-34s %-18s %-17s%s  %s\n", a.Path, a.Type, mode, sens, desc)
		})
	}
	if filter == "" || filter == "provider" {
		show(p.Provider)
	}
	for _, n := range p.resourceNames() {
		if filter == "" || strings.Contains(n, filter) {
			show(p.Resources[n])
		}
	}
}
