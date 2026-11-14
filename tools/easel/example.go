package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// A composed example is HCL the painter never types as HCL. Each attribute is set one at
// a time and checked against the provider schema; references must point at a block the
// example declares and an attribute that block's resource has.

type exAttr struct {
	Path string `json:"path"`
	Expr string `json:"expr"`
}

type exBlockT struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Labels []string `json:"labels,omitempty"`
	Remark []string `json:"remark,omitempty"`
	Attrs  []exAttr `json:"attrs,omitempty"`
}

type example struct {
	Name   string     `json:"name"`
	Seq    int        `json:"seq"`
	Blocks []exBlockT `json:"blocks"`
}

var (
	exNameRe = regexp.MustCompile(`^(provider|resources/obs_[a-z0-9_]+|guides/[a-z0-9-]+)$`)
	identRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	resRefRe = regexp.MustCompile(`\b(obs_[a-z0-9_]+)\.([a-z][a-z0-9_]*)\.([a-z][a-z0-9_]*)`)
	locRefRe = regexp.MustCompile(`\b(local|var)\.([a-z][a-z0-9_]*)`)
)

func examplePath(root, name string) string {
	return filepath.Join(canvasDir(root), "examples", filepath.FromSlash(name)+".json")
}

// exampleFile is where a composed example is rendered.
func exampleFile(name string) string {
	switch {
	case name == "provider":
		return "examples/provider/provider.tf"
	case strings.HasPrefix(name, "resources/"):
		return "examples/" + name + "/resource.tf"
	}
	return "examples/" + name + "/main.tf"
}

func loadExample(root, name string, create bool) *example {
	if !exNameRe.MatchString(name) {
		die("an example is provider, resources/obs_<type> or guides/<slug>")
	}
	raw, err := os.ReadFile(examplePath(root, name))
	if err != nil {
		if create {
			return &example{Name: name}
		}
		die("no example %q; add a block first", name)
	}
	e := &example{}
	if err := json.Unmarshal(raw, e); err != nil {
		die("example %s: %v", name, err)
	}
	return e
}

func allExamples(root string) []*example {
	var out []*example
	base := filepath.Join(canvasDir(root), "examples")
	filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		out = append(out, loadExample(root, strings.TrimSuffix(filepath.ToSlash(rel), ".json"), false))
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (e *example) save(root string) { saveJSON(examplePath(root, e.Name), e) }

func (e *example) block(id string) *exBlockT {
	for i := range e.Blocks {
		if e.Blocks[i].ID == id {
			return &e.Blocks[i]
		}
	}
	die("no block [%s] in %s", id, e.Name)
	return nil
}

func exLook(root, name string) {
	e := loadExample(root, name, true)
	fmt.Printf("== ex:%s -> %s  (%d blocks)\n", e.Name, exampleFile(e.Name), len(e.Blocks))
	for _, b := range e.Blocks {
		fmt.Printf("  [%s] %s %s  (%d attrs)\n", b.ID, b.Kind, strings.Join(b.Labels, " "), len(b.Attrs))
	}
}

func exBlock(root string, p *palette, name, kind string, labels []string) {
	e := loadExample(root, name, true)
	want := map[string]int{"terraform": 0, "provider": 0, "locals": 0, "resource": 2, "variable": 1, "output": 1}
	n, ok := want[kind]
	if !ok || len(labels) != n {
		die("block kinds: terraform, provider, locals (no labels); variable, output (name); resource (type name)")
	}
	for _, l := range labels {
		if !identRe.MatchString(l) {
			die("label %q is not an identifier", l)
		}
	}
	if kind == "resource" && p.Resources[labels[0]] == nil {
		die("the provider has no resource %s", labels[0])
	}
	for _, b := range e.Blocks {
		if b.Kind == kind && strings.Join(b.Labels, ".") == strings.Join(labels, ".") {
			die("%s %s is already declared at [%s]", kind, strings.Join(labels, "."), b.ID)
		}
	}
	spend(root, "ex:"+name)
	e.Seq++
	id := fmt.Sprintf("b%d", e.Seq)
	e.Blocks = append(e.Blocks, exBlockT{ID: id, Kind: kind, Labels: labels})
	e.save(root)
	fmt.Printf("[%s] %s %s\n", id, kind, strings.Join(labels, " "))
}

// settable resolves a dotted path against a schema. Under a map of nested attributes the
// segment after the map is the author's own key.
func settable(s *schemaOf, path string) error {
	parts := strings.Split(path, ".")
	list := s.Attrs
	for i := 0; i < len(parts); i++ {
		var hit *attr
		for _, a := range list {
			if a.Name == parts[i] {
				hit = a
			}
		}
		if hit == nil {
			return fmt.Errorf("%s has no attribute %q", s.Type, strings.Join(parts[:i+1], "."))
		}
		if !hit.Required && !hit.Optional {
			return fmt.Errorf("%s is read-only; OBS decides it", strings.Join(parts[:i+1], "."))
		}
		if i == len(parts)-1 {
			return nil
		}
		switch hit.Nesting {
		case "single":
		case "map":
			i++ // the key
			if i == len(parts)-1 {
				return fmt.Errorf("%s is one entry of a map; set its attributes", path)
			}
		case "list":
			i++ // the element's position
			if _, err := strconv.Atoi(parts[i]); err != nil || i == len(parts)-1 {
				return fmt.Errorf("%s: after a list comes an element number, then one of its attributes", path)
			}
		default:
			return fmt.Errorf("%s cannot be set by path; set %s as a whole", path, strings.Join(parts[:i+1], "."))
		}
		list = hit.Children
	}
	return nil
}

func exSet(root string, p *palette, name, id, path, expr string) {
	e := loadExample(root, name, false)
	b := e.block(id)
	if expr == "" || strings.ContainsAny(expr, "\n\r") || len(expr) > 200 {
		die("an expression is one line of at most 200 characters")
	}
	for _, part := range strings.Split(path, ".") {
		if !regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*|[0-9]+)$`).MatchString(part) {
			die("bad path %q", path)
		}
	}
	switch b.Kind {
	case "terraform":
		die("the terraform block is laid by the easel")
	case "resource":
		if path != "depends_on" {
			if err := settable(p.Resources[b.Labels[0]], path); err != nil {
				die("%v", err)
			}
		}
	case "provider":
		if err := settable(p.Provider, path); err != nil {
			die("%v", err)
		}
	case "variable":
		if path != "type" && path != "default" && path != "description" && path != "sensitive" {
			die("a variable takes type, default, description, sensitive")
		}
	case "output":
		if path != "value" && path != "description" && path != "sensitive" {
			die("an output takes value, description, sensitive")
		}
	}
	for _, a := range b.Attrs {
		if a.Path == path || strings.HasPrefix(path, a.Path+".") || strings.HasPrefix(a.Path, path+".") {
			die("%s is already set (%s); scrape %s:%s first", path, a.Path, id, a.Path)
		}
	}
	for _, m := range resRefRe.FindAllStringSubmatch(expr, -1) {
		found := false
		for _, o := range e.Blocks {
			if o.Kind == "resource" && o.Labels[0] == m[1] && o.Labels[1] == m[2] {
				found = true
			}
		}
		if !found {
			die("%s.%s is not declared in this example", m[1], m[2])
		}
		if m[3] != "id" && p.Resources[m[1]].find(m[3]) == nil {
			die("%s has no attribute %s", m[1], m[3])
		}
	}
	for _, m := range locRefRe.FindAllStringSubmatch(expr, -1) {
		found := false
		for _, o := range e.Blocks {
			if m[1] == "var" && o.Kind == "variable" && o.Labels[0] == m[2] {
				found = true
			}
			if m[1] == "local" && o.Kind == "locals" {
				for _, a := range o.Attrs {
					found = found || a.Path == m[2] || strings.HasPrefix(a.Path, m[2]+".")
				}
			}
		}
		if !found {
			die("%s.%s is not declared in this example", m[1], m[2])
		}
	}
	spend(root, "ex:"+name)
	b.Attrs = append(b.Attrs, exAttr{path, expr})
	e.save(root)
	fmt.Printf("[%s] %s = %s\n", id, path, expr)
}

func exRemark(root, name, id, text string) {
	e := loadExample(root, name, false)
	b := e.block(id)
	plain(text, 18)
	if strings.Contains(text, "`") || len(b.Remark) >= 3 {
		die("a remark is plain words, at most three lines per block")
	}
	spend(root, "ex:"+name)
	b.Remark = append(b.Remark, text)
	e.save(root)
	fmt.Printf("[%s] # %s\n", id, text)
}

func exScrape(root, name, id string) {
	e := loadExample(root, name, false)
	spend(root, "ex:"+name)
	if bid, path, ok := strings.Cut(id, ":"); ok {
		b := e.block(bid)
		if path == "remark" {
			b.Remark = nil
			e.save(root)
			fmt.Printf("scraped remarks of [%s]\n", bid)
			return
		}
		for i, a := range b.Attrs {
			if a.Path == path {
				b.Attrs = append(b.Attrs[:i], b.Attrs[i+1:]...)
				e.save(root)
				fmt.Printf("scraped %s\n", id)
				return
			}
		}
		die("no %s in [%s]", path, bid)
	}
	for i, b := range e.Blocks {
		if b.ID == id {
			e.Blocks = append(e.Blocks[:i], e.Blocks[i+1:]...)
			e.save(root)
			fmt.Printf("scraped [%s]\n", id)
			return
		}
	}
	die("no block [%s]", id)
}

// --- HCL rendering, in terraform fmt's layout -------------------------------------

type node struct {
	name string
	expr string
	kids []*node
}

func tree(attrs []exAttr) []*node {
	var top []*node
	for _, a := range attrs {
		list := &top
		parts := strings.Split(a.Path, ".")
		for i, part := range parts {
			var hit *node
			for _, n := range *list {
				if n.name == part {
					hit = n
				}
			}
			if hit == nil {
				hit = &node{name: part}
				*list = append(*list, hit)
			}
			if i == len(parts)-1 {
				hit.expr = a.Expr
			}
			list = &hit.kids
		}
	}
	return top
}

// isList reports whether every child is a numbered element (a list of objects).
func isList(kids []*node) bool {
	for _, k := range kids {
		if _, err := strconv.Atoi(k.name); err != nil {
			return false
		}
	}
	return len(kids) > 0
}

func writeBody(b *strings.Builder, nodes []*node, indent string) {
	for i := 0; i < len(nodes); {
		if isList(nodes[i].kids) {
			fmt.Fprintf(b, "%s%s = [\n", indent, nodes[i].name)
			for n, el := range nodes[i].kids {
				fmt.Fprintf(b, "%s  {\n", indent)
				writeBody(b, el.kids, indent+"    ")
				sep := ","
				if n == len(nodes[i].kids)-1 {
					sep = ""
				}
				fmt.Fprintf(b, "%s  }%s\n", indent, sep)
			}
			fmt.Fprintf(b, "%s]\n", indent)
			i++
			continue
		}
		if len(nodes[i].kids) > 0 {
			fmt.Fprintf(b, "%s%s = {\n", indent, nodes[i].name)
			writeBody(b, nodes[i].kids, indent+"  ")
			fmt.Fprintf(b, "%s}\n", indent)
			i++
			continue
		}
		j, width := i, 0
		for j < len(nodes) && len(nodes[j].kids) == 0 {
			if len(nodes[j].name) > width {
				width = len(nodes[j].name)
			}
			j++
		}
		for ; i < j; i++ {
			fmt.Fprintf(b, "%s%-*s = %s\n", indent, width, nodes[i].name, nodes[i].expr)
		}
	}
}

func (e *example) hcl() string {
	var b strings.Builder
	for i, blk := range e.Blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, r := range blk.Remark {
			fmt.Fprintf(&b, "# %s\n", r)
		}
		switch blk.Kind {
		case "terraform":
			b.WriteString("terraform {\n  required_providers {\n    obs = { source = \"digibugcat/obs\" }\n  }\n}\n")
			continue
		case "provider":
			b.WriteString("provider \"obs\" {")
		case "locals":
			b.WriteString("locals {")
		default:
			b.WriteString(blk.Kind)
			for _, l := range blk.Labels {
				fmt.Fprintf(&b, " %q", l)
			}
			b.WriteString(" {")
		}
		if len(blk.Attrs) == 0 {
			b.WriteString("}\n")
			continue
		}
		b.WriteString("\n")
		writeBody(&b, tree(blk.Attrs), "  ")
		b.WriteString("}\n")
	}
	return b.String()
}

// --- proof -------------------------------------------------------------------------

func proof(root string) {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	tf := filepath.Join(root, ".tools", "terraform", "1.15.7", "terraform"+exe)
	if _, err := os.Stat(tf); err != nil {
		if tf, err = exec.LookPath("terraform"); err != nil {
			die("no terraform binary (looked in .tools/terraform/1.15.7 and PATH)")
		}
	}
	work, err := os.MkdirTemp("", "easel-proof-")
	if err != nil {
		die("%v", err)
	}
	defer os.RemoveAll(work)
	build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(work, "terraform-provider-obs"+exe), ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		die("building the provider: %v\n%s", err, out)
	}
	rc := filepath.Join(work, "terraform.rc")
	body := fmt.Sprintf("provider_installation {\n  dev_overrides {\n    %q = %q\n  }\n  direct {}\n}\n", "registry.terraform.io/digibugcat/obs", filepath.ToSlash(work))
	os.WriteFile(rc, []byte(body), 0o644)
	bad := 0
	for _, e := range allExamples(root) {
		dir := filepath.Join(work, strings.ReplaceAll(e.Name, "/", "_"))
		os.MkdirAll(dir, 0o755)
		src := e.hcl()
		if !strings.Contains(src, "required_providers") {
			src = "terraform {\n  required_providers {\n    obs = { source = \"digibugcat/obs\" }\n  }\n}\n\n" + src
		}
		os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644)
		for _, args := range [][]string{{"fmt", "-check", "-diff"}, {"validate", "-no-color"}} {
			cmd := exec.Command(tf, args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc)
			if out, err := cmd.CombinedOutput(); err != nil {
				bad++
				fmt.Printf("FAIL %s: terraform %s\n%s\n", e.Name, args[0], out)
			}
		}
		fmt.Printf("proved %s\n", e.Name)
	}
	if bad > 0 {
		die("%d example check(s) failed", bad)
	}
}
