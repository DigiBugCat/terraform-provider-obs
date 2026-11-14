package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const docsURL = "https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs"

type renderer struct {
	root   string
	p      *palette
	pages  map[string]*canvas
	mixes  map[string]string
	issues []string
}

// describe is what the schema section says about one attribute: the page's own glaze,
// else the provider's description, else a shared mix.
func (r *renderer) describe(c *canvas, a *attr) string {
	if g := c.Glazes[a.Path]; g != "" {
		return g
	}
	if a.Desc != "" {
		return a.Desc
	}
	if c.Page != "index" {
		if m := mixFor(r.mixes, a.Path); m != "" {
			return m
		}
	}
	if a.Path == "id" {
		return "The ID of this resource."
	}
	return ""
}

func (r *renderer) inline(text string, linked bool) string {
	return refRe.ReplaceAllStringFunc(text, func(m string) string {
		page := m[1:]
		name, url := "the provider configuration", docsURL
		if rest, ok := strings.CutPrefix(page, "resources/"); ok {
			name, url = "`obs_"+rest+"`", docsURL+"/resources/"+rest
		} else if slug, ok := strings.CutPrefix(page, "guides/"); ok {
			c := r.pages[page]
			if c == nil {
				r.issues = append(r.issues, "a stroke refers to @"+page+", which is not stretched")
				return page
			}
			name, url = c.Title, docsURL+"/guides/"+slug
		}
		if !linked {
			return name
		}
		return "[" + name + "](" + url + ")"
	})
}

// lead is the page's opening paragraph: the line strokes before anything else.
func (r *renderer) lead(c *canvas) string {
	var parts []string
	for i, s := range c.Strokes {
		if s.Brush != "line" || (s.New && i > 0) {
			break
		}
		parts = append(parts, s.Text)
	}
	return r.inline(strings.Join(parts, " "), false)
}

func firstSentence(text string) string {
	bare := []byte(text)
	in := false
	for i := 0; i < len(bare)-1; i++ {
		if bare[i] == '`' {
			in = !in
		}
		if !in && bare[i] == '.' && bare[i+1] == ' ' {
			return text[:i+1]
		}
	}
	return text
}

func (r *renderer) schema(c *canvas, s *schemaOf) string {
	var b strings.Builder
	b.WriteString("## Schema\n")
	var nested []*attr
	item := func(a *attr) string {
		t := a.Type
		if a.Sensitive {
			t += ", Sensitive"
		}
		line := fmt.Sprintf("- `%s` (%s)", a.Name, t)
		desc := r.describe(c, a)
		if desc != "" {
			line += " " + r.inline(desc, true)
		}
		if len(a.Children) > 0 {
			line += fmt.Sprintf(" (see [below for nested schema](#nestedatt--%s))", strings.ReplaceAll(a.Path, ".", "--"))
			nested = append(nested, a)
		}
		return line
	}
	groups := func(attrs []*attr, head func(string) string) {
		var req, opt, ro []string
		for _, a := range attrs {
			switch {
			case a.Required:
				req = append(req, item(a))
			case a.Optional:
				opt = append(opt, item(a))
			default:
				ro = append(ro, item(a))
			}
		}
		for _, g := range []struct {
			name  string
			lines []string
		}{{"Required", req}, {"Optional", opt}, {"Read-Only", ro}} {
			if len(g.lines) > 0 {
				b.WriteString("\n" + head(g.name) + "\n\n" + strings.Join(g.lines, "\n") + "\n")
			}
		}
	}
	groups(s.Attrs, func(n string) string { return "### " + n })
	for i := 0; i < len(nested); i++ {
		a := nested[i]
		fmt.Fprintf(&b, "\n<a id=\"nestedatt--%s\"></a>\n### Nested Schema for `%s`\n", strings.ReplaceAll(a.Path, ".", "--"), a.Path)
		groups(a.Children, func(n string) string { return n + ":" })
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *renderer) roster() string {
	var b strings.Builder
	b.WriteString("| Resource | Manages |\n|---|---|\n")
	for _, n := range r.p.resourceNames() {
		short := strings.TrimPrefix(n, "obs_")
		what := ""
		if c := r.pages["resources/"+short]; c != nil {
			what = firstSentence(r.lead(c))
		}
		fmt.Fprintf(&b, "| [`%s`](%s/resources/%s) | %s |\n", n, docsURL, short, what)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *renderer) exampleText(ref string) string {
	if name, ok := strings.CutPrefix(ref, "ex:"); ok {
		return loadExample(r.root, name, false).hcl()
	}
	raw, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(strings.TrimPrefix(ref, "file:"))))
	if err != nil {
		r.issues = append(r.issues, "example "+ref+" is gone")
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

func (r *renderer) page(c *canvas) string {
	title, heading := c.Title, c.Title
	switch {
	case c.Page == "index":
		title, heading = "obs Provider", "obs Provider"
	case strings.HasPrefix(c.Page, "resources/"):
		n := "obs_" + strings.TrimPrefix(c.Page, "resources/")
		title, heading = n+" Resource - obs", n+" (Resource)"
	}
	var b strings.Builder
	if c.Page == "readme" {
		// The repository front page is plain markdown: no registry front matter.
		fmt.Fprintf(&b, "# %s\n", heading)
	} else {
		fmt.Fprintf(&b, "---\npage_title: %q\n", title)
		if c.Page != "index" {
			fmt.Fprintf(&b, "subcategory: %q\n", c.Subcategory)
		}
		fmt.Fprintf(&b, "description: |-\n  %s\n---\n\n# %s\n", r.lead(c), heading)
	}

	var blocks []string
	ss := c.Strokes
	for i := 0; i < len(ss); {
		s := ss[i]
		run := func(same func(strokeT, int) bool) []strokeT {
			j := i + 1
			for j < len(ss) && ss[j].Brush == s.Brush && same(ss[j], j) {
				j++
			}
			out := ss[i:j]
			i = j
			return out
		}
		always := func(strokeT, int) bool { return true }
		switch s.Brush {
		case "line":
			var parts []string
			for _, x := range run(func(x strokeT, _ int) bool { return !x.New }) {
				parts = append(parts, r.inline(x.Text, true))
			}
			blocks = append(blocks, strings.Join(parts, " "))
		case "bullet", "step":
			var lines []string
			for n, x := range run(always) {
				mark := "-"
				if s.Brush == "step" {
					mark = fmt.Sprintf("%d.", n+1)
				}
				lines = append(lines, mark+" "+r.inline(x.Text, true))
			}
			blocks = append(blocks, strings.Join(lines, "\n"))
		case "note", "warn":
			mark := "->"
			if s.Brush == "warn" {
				mark = "~>"
			}
			blocks = append(blocks, mark+" "+r.inline(s.Text, true))
			i++
		case "heading":
			blocks = append(blocks, strings.Repeat("#", s.Level)+" "+s.Text)
			i++
		case "row":
			rows := run(always)
			var lines []string
			width := len(strings.Split(rows[0].Text, " | "))
			for n, x := range rows {
				cells := strings.Split(r.inline(x.Text, true), " | ")
				if len(cells) != width {
					r.issues = append(r.issues, fmt.Sprintf("%s [%s]: row has %d cells, the table has %d", c.Page, x.ID, len(cells), width))
				}
				lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
				if n == 0 {
					lines = append(lines, "|"+strings.Repeat("---|", width))
				}
			}
			blocks = append(blocks, strings.Join(lines, "\n"))
		case "shell", "literal":
			lang := "shell"
			if s.Brush == "literal" {
				lang = "text"
			}
			var lines []string
			for _, x := range run(always) {
				lines = append(lines, x.Text)
			}
			blocks = append(blocks, "```"+lang+"\n"+strings.Join(lines, "\n")+"\n```")
		case "example":
			blocks = append(blocks, "```terraform\n"+strings.TrimRight(r.exampleText(s.Text), "\n")+"\n```")
			i++
		case "schema":
			blocks = append(blocks, r.schema(c, r.p.schemaFor(c.Page)))
			i++
		case "roster":
			blocks = append(blocks, r.roster())
			i++
		default:
			i++
		}
	}
	for _, blk := range blocks {
		b.WriteString("\n" + blk + "\n")
	}
	return b.String()
}

func docFile(page string) string {
	if page == "index" {
		return "docs/index.md"
	}
	if page == "readme" {
		return "README.md"
	}
	return "docs/" + page + ".md"
}

// render writes every canvas to its file (write=true) or reports the files that differ.
func render(root string, p *palette, write bool) int {
	r := &renderer{root: root, p: p, pages: map[string]*canvas{}, mixes: loadMixes(root)}
	cs := allCanvases(root)
	for _, c := range cs {
		r.pages[c.Page] = c
	}
	out := map[string]string{}
	for _, c := range cs {
		out[docFile(c.Page)] = r.page(c)
	}
	for _, e := range allExamples(root) {
		out[exampleFile(e.Name)] = e.hcl()
	}
	diff := 0
	paths := make([]string, 0, len(out))
	for k := range out {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		full := filepath.Join(root, filepath.FromSlash(rel))
		have, _ := os.ReadFile(full)
		if strings.ReplaceAll(string(have), "\r\n", "\n") == out[rel] {
			continue
		}
		diff++
		if !write {
			fmt.Printf("differs: %s\n", rel)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(out[rel]), 0o644); err != nil {
			die("%v", err)
		}
		fmt.Printf("rendered %s\n", rel)
	}
	// A page in the rendered directories with no canvas was written by hand.
	for _, dir := range []string{"docs/resources", "docs/guides"} {
		entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		for _, en := range entries {
			rel := dir + "/" + en.Name()
			if _, ok := out[rel]; !ok && strings.HasSuffix(rel, ".md") {
				fmt.Printf("no canvas: %s\n", rel)
				if !write {
					diff++
				}
			}
		}
	}
	for _, is := range r.issues {
		fmt.Println("issue:", is)
	}
	if write {
		fmt.Printf("%d file(s) written, %d page(s), %d example(s)\n", diff, len(cs), len(out)-len(cs))
		return len(r.issues)
	}
	return diff + len(r.issues)
}

// critique lists what a complete set of pages still lacks.
func critique(root string, p *palette, only string, print bool) int {
	r := &renderer{root: root, p: p, pages: map[string]*canvas{}, mixes: loadMixes(root)}
	for _, c := range allCanvases(root) {
		r.pages[c.Page] = c
	}
	var issues []string
	say := func(page, format string, a ...any) {
		if only == "" || only == page {
			issues = append(issues, page+": "+fmt.Sprintf(format, a...))
		}
	}
	want := []string{"index"}
	for _, n := range p.resourceNames() {
		want = append(want, "resources/"+strings.TrimPrefix(n, "obs_"))
	}
	for _, page := range want {
		if r.pages[page] == nil {
			say(page, "no canvas")
		}
	}
	pages := make([]string, 0, len(r.pages))
	for k := range r.pages {
		pages = append(pages, k)
	}
	sort.Strings(pages)
	for _, page := range pages {
		c := r.pages[page]
		has := map[string]int{}
		heads := map[string]bool{}
		for _, s := range c.Strokes {
			has[s.Brush]++
			if s.Brush == "heading" {
				heads[s.Text] = true
			}
		}
		if r.lead(c) == "" {
			say(page, "does not open with a line")
		}
		sc := p.schemaFor(page)
		if sc != nil {
			if has["example"] == 0 {
				say(page, "no example")
			}
			if has["schema"] == 0 {
				say(page, "no schema stroke")
			}
			if sc.Importable && !heads["Import"] {
				say(page, "importable, but no Import heading")
			}
			if sc.Importable && has["literal"]+has["shell"] == 0 {
				say(page, "importable, but the import form is not shown")
			}
			var bare []string
			sc.walk(func(a *attr) {
				if r.describe(c, a) == "" {
					bare = append(bare, a.Path)
				}
			})
			if len(bare) > 0 {
				say(page, "%d bare attribute(s): %s", len(bare), strings.Join(bare, " "))
			}
			for path := range c.Glazes {
				if sc.find(path) == nil {
					say(page, "glaze on %s, which the schema no longer has", path)
				}
			}
		} else if has["heading"] == 0 || len(c.Strokes) < 8 {
			say(page, "a guide needs headings and at least 8 strokes")
		}
		r.issues = nil
		r.page(c)
		for _, is := range r.issues {
			say(page, "%s", is)
		}
	}
	if print {
		for _, is := range issues {
			fmt.Println(is)
		}
		fmt.Printf("%d issue(s) across %d page(s)\n", len(issues), len(pages))
	}
	return len(issues)
}
