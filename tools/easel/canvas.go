package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type strokeT struct {
	ID    string `json:"id"`
	Brush string `json:"brush"`
	Text  string `json:"text,omitempty"`
	Level int    `json:"level,omitempty"`
	New   bool   `json:"new,omitempty"` // line: begins a new paragraph
}

type canvas struct {
	Page        string            `json:"page"`
	Title       string            `json:"title,omitempty"`
	Subcategory string            `json:"subcategory,omitempty"`
	Seq         int               `json:"seq"`
	Strokes     []strokeT         `json:"strokes"`
	Glazes      map[string]string `json:"glazes,omitempty"`
}

const strokesPerLook = 4

var (
	pageRe  = regexp.MustCompile(`^(index|readme|resources/[a-z0-9_]+|guides/[a-z0-9-]+)$`)
	refRe   = regexp.MustCompile(`@(index|resources/[a-z0-9_]+|guides/[a-z0-9-]+)`)
	tickRe  = regexp.MustCompile("`([^`]*)`")
	urlRe   = regexp.MustCompile(`https?://[^\s)>\]]+`)
	manyRe  = regexp.MustCompile(`[.!?]["')]?\s+[A-Z]`)
	tfCmdRe = regexp.MustCompile(`^terraform( -chdir=\S+)? (init|plan|apply|destroy|import|validate|fmt|show|output|get|state (rm|mv|list|show|replace-provider)|providers schema)( .*)?$`)
)

// cliWords are Terraform's own vocabulary, which no provider source has to contain.
var cliWords = map[string]bool{"terraform": true, "~/.terraformrc": true, "terraform.rc": true, "dev_overrides": true,
	"required_providers": true, "depends_on": true, "jsonencode": true, "moved": true, "import": true, "for_each": true,
	"TF_CLI_CONFIG_FILE": true, "provider_installation": true, "sensitive": true, "lifecycle": true, "ignore_changes": true,
	"-refresh=false": true, "-target": true, "null": true, "true": true, "false": true, "id": true, "terraform.tfstate": true}

func canvasDir(root string) string { return filepath.Join(root, "tools", "easel", "canvas") }

func canvasPath(root, page string) string {
	return filepath.Join(canvasDir(root), filepath.FromSlash(page)+".json")
}

func loadCanvas(root, page string) *canvas {
	raw, err := os.ReadFile(canvasPath(root, page))
	if err != nil {
		die("no canvas %q; stretch it first", page)
	}
	c := &canvas{}
	if err := json.Unmarshal(raw, c); err != nil {
		die("canvas %s: %v", page, err)
	}
	if c.Glazes == nil {
		c.Glazes = map[string]string{}
	}
	return c
}

func saveJSON(path string, v any) {
	raw, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		die("%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		die("%v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		die("%v", err)
	}
}

func (c *canvas) save(root string) { saveJSON(canvasPath(root, c.Page), c) }

func allCanvases(root string) []*canvas {
	var out []*canvas
	base := canvasDir(root)
	filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".json" || strings.HasPrefix(info.Name(), "_") {
			if err == nil && info.IsDir() && (info.Name() == "examples" || info.Name() == "diagrams") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		out = append(out, loadCanvas(root, strings.TrimSuffix(filepath.ToSlash(rel), ".json")))
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Page < out[j].Page })
	return out
}

// --- stepping back -----------------------------------------------------------------

type wetT struct {
	Target string `json:"target"`
	Left   int    `json:"left"`
}

func wetPath(root string) string { return filepath.Join(canvasDir(root), ".wet") }

func wet(root, target string) {
	saveJSON(wetPath(root), wetT{Target: target, Left: strokesPerLook})
}

// spend takes one stroke from the wet canvas, or refuses.
func spend(root, target string) {
	var w wetT
	raw, _ := os.ReadFile(wetPath(root))
	json.Unmarshal(raw, &w)
	if w.Target != target || w.Left <= 0 {
		die("the canvas is dry: step back and `easel look %s` before the next stroke", target)
	}
	w.Left--
	saveJSON(wetPath(root), w)
}

func stretch(root string, p *palette, page, title, sub string) {
	if !pageRe.MatchString(page) {
		die("a page is index, readme, resources/<name> or guides/<slug>")
	}
	if _, err := os.Stat(canvasPath(root, page)); err == nil {
		die("canvas %s is already stretched", page)
	}
	c := &canvas{Page: page, Subcategory: sub, Glazes: map[string]string{}}
	switch {
	case strings.HasPrefix(page, "guides/"), page == "readme":
		if title == "" || len(strings.Fields(title)) > 8 {
			die("a guide or the readme needs --title of at most 8 words")
		}
		c.Title = title
	default:
		if title != "" {
			die("the title of %s comes from the provider", page)
		}
		if p.schemaFor(page) == nil {
			die("the provider has no %s", page)
		}
	}
	c.save(root)
	fmt.Printf("stretched %s\n", page)
}

func look(root, target string, all bool) {
	if name, ok := strings.CutPrefix(target, "ex:"); ok {
		exLook(root, name)
		wet(root, target)
		return
	}
	if target == "mix" {
		lookMixes(root)
		wet(root, target)
		return
	}
	if name, ok := strings.CutPrefix(target, "dg:"); ok {
		dgLook(root, name)
		wet(root, target)
		return
	}
	c := loadCanvas(root, target)
	fmt.Printf("== %s  %q  [%s]  %d strokes, %d glazes\n", c.Page, c.Title, c.Subcategory, len(c.Strokes), len(c.Glazes))
	from := 0
	if !all && len(c.Strokes) > 6 {
		from = len(c.Strokes) - 6
		fmt.Printf("   ... %d earlier strokes (--all)\n", from)
	}
	for _, s := range c.Strokes[from:] {
		mark := ""
		if s.New {
			mark = " ¶"
		}
		if s.Level > 0 {
			mark = fmt.Sprintf(" h%d", s.Level)
		}
		fmt.Printf("  [%s] %s%s: %s\n", s.ID, s.Brush, mark, s.Text)
	}
	if all {
		keys := make([]string, 0, len(c.Glazes))
		for k := range c.Glazes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  glaze %s: %s\n", k, c.Glazes[k])
		}
	}
	wet(root, target)
}

// --- the rules every brush obeys ---------------------------------------------------

func verifyTicks(p *palette, text string) {
	if strings.Count(text, "`")%2 != 0 {
		die("unbalanced backticks")
	}
	names := p.attrNames()
	for _, m := range tickRe.FindAllStringSubmatch(text, -1) {
		tok := m[1]
		if tok == "" {
			die("empty backticks")
		}
		last := tok[strings.LastIndex(tok, ".")+1:]
		switch {
		case p.Resources[tok] != nil, names[tok], names[last] && !strings.ContainsAny(tok, " /"), cliWords[tok],
			tfCmdRe.MatchString(tok), strings.Contains(p.corpus, tok):
		default:
			die("`%s` is not on the palette: it is not a resource, an attribute, or anything in the shipped source", tok)
		}
	}
}

func verifyRefs(p *palette, text string) {
	for _, m := range refRe.FindAllStringSubmatch(text, -1) {
		if rest, ok := strings.CutPrefix(m[1], "resources/"); ok && p.Resources["obs_"+rest] == nil {
			die("@%s: the provider has no obs_%s", m[1], rest)
		}
	}
	for _, u := range urlRe.FindAllString(text, -1) {
		u = strings.TrimRight(u, ".,;:")
		if !strings.Contains(p.corpus, u) && !strings.HasPrefix(u, "https://github.com/DigiBugCat/terraform-provider-obs") &&
			!strings.HasPrefix(u, "https://obsproject.com") && !strings.HasPrefix(u, "https://developer.hashicorp.com/terraform") {
			die("%s is not a link the source knows", u)
		}
	}
}

func plain(text string, max int) {
	if text == "" || text != strings.TrimSpace(text) || strings.ContainsAny(text, "\n\r\t") {
		die("a stroke is one line of text with no leading or trailing space")
	}
	if n := len(strings.Fields(text)); n > max {
		die("%d words; this brush holds %d. Use two strokes", n, max)
	}
	bare := tickRe.ReplaceAllString(text, "x")
	if strings.ContainsAny(bare, "|<>") || strings.Contains(bare, "  ") {
		die("no pipes, angle brackets or double spaces outside backticks")
	}
	if strings.ContainsAny(text[:1], "#-*>+") {
		die("a stroke does not carry its own markdown; pick the brush instead")
	}
}

func sentence(p *palette, text string, max int) {
	plain(text, max)
	bare := tickRe.ReplaceAllString(text, "x")
	if !strings.HasSuffix(bare, ".") && !strings.HasSuffix(bare, ":") && !strings.HasSuffix(bare, "?") {
		die("a sentence ends with a period, a colon or a question mark")
	}
	if manyRe.MatchString(bare) {
		die("one sentence per stroke")
	}
	verifyTicks(p, text)
	verifyRefs(p, text)
}

var shellFirst = map[string]bool{"terraform": true, "go": true, "make": true, "export": true, "cd": true, "cp": true, "mkdir": true, "git": true}

func stroke(root string, p *palette, page, brush, text string, fl map[string]string) {
	c := loadCanvas(root, page)
	s := strokeT{Brush: brush, Text: text}
	count := func(b string) (n int) {
		for _, x := range c.Strokes {
			if x.Brush == b {
				n++
			}
		}
		return
	}
	switch brush {
	case "line":
		sentence(p, text, 30)
		s.New = fl["new"] != ""
	case "bullet", "step":
		sentence(p, text, 30)
	case "note", "warn":
		sentence(p, text, 34)
	case "heading":
		plain(text, 7)
		if strings.ContainsAny(text[len(text)-1:], ".:;,") {
			die("a heading has no closing punctuation")
		}
		verifyTicks(p, text)
		s.Level = 2
		if fl["level"] == "3" {
			s.Level = 3
		} else if fl["level"] != "" && fl["level"] != "2" {
			die("headings are level 2 or 3")
		}
	case "row":
		cells := strings.Split(text, " | ")
		if len(cells) < 2 {
			die("a row is cells separated by ' | '")
		}
		for _, cell := range cells {
			plain(cell, 18)
		}
		verifyTicks(p, text)
		verifyRefs(p, text)
	case "shell":
		if text == "" || strings.ContainsAny(text, "\n\r`") || len(text) > 240 {
			die("a shell stroke is one command of at most 240 characters")
		}
		if !shellFirst[strings.Fields(text)[0]] {
			die("a shell stroke starts with one of: terraform go make export cd cp mkdir git")
		}
	case "literal":
		if text == "" || strings.ContainsAny(text, "\n\r") || !strings.Contains(p.corpus, text) {
			die("a literal is quoted verbatim from the shipped source; this text is not in it")
		}
	case "example":
		if name, ok := strings.CutPrefix(text, "ex:"); ok {
			if _, err := os.Stat(examplePath(root, name)); err != nil {
				die("no composed example %q", name)
			}
		} else if file, ok := strings.CutPrefix(text, "file:"); ok {
			if !strings.HasPrefix(file, "examples/") || strings.Contains(file, "..") {
				die("an example file lives under examples/")
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err != nil {
				die("no file %s", file)
			}
		} else {
			die("an example stroke is ex:<composed name> or file:examples/<path>")
		}
	case "picture":
		// "<diagram> | <one sentence saying what the picture shows>"
		name, alt, ok := strings.Cut(text, " | ")
		if !ok {
			die("a picture stroke is: <diagram> | <sentence describing it>")
		}
		if _, err := os.Stat(diagramPath(root, name)); err != nil {
			die("no diagram %q", name)
		}
		sentence(p, alt, 30)
		if strings.ContainsAny(alt, "`[]") {
			die("the description of a picture is plain words")
		}
	case "schema":
		if text != "" || p.schemaFor(page) == nil || count("schema") > 0 {
			die("the schema brush takes no text, once, on index or a resource page")
		}
	case "roster":
		if text != "" || (page != "index" && page != "readme") || count("roster") > 0 {
			die("the roster brush takes no text, once, on index or readme")
		}
	default:
		die("no brush %q", brush)
	}
	for _, x := range c.Strokes {
		if text != "" && x.Text == text && x.Brush == brush {
			die("that exact stroke is already on the canvas at [%s]", x.ID)
		}
	}
	spend(root, page)
	c.Seq++
	s.ID = fmt.Sprintf("s%d", c.Seq)
	at := len(c.Strokes)
	if fl["first"] != "" {
		at = 0
	} else if after := fl["after"]; after != "" {
		at = -1
		for i, x := range c.Strokes {
			if x.ID == after {
				at = i + 1
			}
		}
		if at < 0 {
			die("no stroke [%s] on %s", after, page)
		}
	}
	c.Strokes = append(c.Strokes[:at], append([]strokeT{s}, c.Strokes[at:]...)...)
	c.save(root)
	fmt.Printf("[%s] %s\n", s.ID, brush)
}

func glaze(root string, p *palette, page, path, text string) {
	c := loadCanvas(root, page)
	sc := p.schemaFor(page)
	if sc == nil || sc.find(path) == nil {
		die("%s has no attribute %s", page, path)
	}
	if _, dup := c.Glazes[path]; dup {
		die("%s is already glazed; scrape glaze:%s first", path, path)
	}
	plain(text, 40)
	if !strings.HasSuffix(text, ".") {
		die("a glaze ends with a period")
	}
	verifyTicks(p, text)
	verifyRefs(p, text)
	spend(root, page)
	c.Glazes[path] = text
	c.save(root)
	fmt.Printf("glazed %s\n", path)
}

func scrape(root, target, id string) {
	if name, ok := strings.CutPrefix(target, "ex:"); ok {
		exScrape(root, name, id)
		return
	}
	if name, ok := strings.CutPrefix(target, "dg:"); ok {
		dgScrape(root, name, id)
		return
	}
	if target == "mix" {
		mixes := loadMixes(root)
		if _, ok := mixes[id]; !ok {
			die("no mix %s", id)
		}
		spend(root, "mix")
		delete(mixes, id)
		saveJSON(mixPath(root), mixes)
		fmt.Printf("scraped mix %s\n", id)
		return
	}
	c := loadCanvas(root, target)
	spend(root, target)
	if path, ok := strings.CutPrefix(id, "glaze:"); ok {
		if _, has := c.Glazes[path]; !has {
			die("no glaze on %s", path)
		}
		delete(c.Glazes, path)
		c.save(root)
		fmt.Printf("scraped glaze %s\n", path)
		return
	}
	for i, x := range c.Strokes {
		if x.ID == id {
			c.Strokes = append(c.Strokes[:i], c.Strokes[i+1:]...)
			c.save(root)
			fmt.Printf("scraped [%s]\n", id)
			return
		}
	}
	die("no stroke [%s] on %s", id, target)
}

// --- mixes ---------------------------------------------------------------------------
//
// A mix is one description shared by every attribute whose path ends with its key
// ("collection", "transform.x"). The provider repeats the same placement fields on many
// resources; a mix says what the field is once. A glaze on a page, or a description in
// the provider schema itself, wins over a mix.

func mixPath(root string) string { return filepath.Join(canvasDir(root), "_mixes.json") }

func loadMixes(root string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(mixPath(root))
	if err == nil {
		if err := json.Unmarshal(raw, &out); err != nil {
			die("mixes: %v", err)
		}
	}
	return out
}

func mixMatches(key, path string) bool { return path == key || strings.HasSuffix(path, "."+key) }

// mixFor returns the mix with the longest key that covers path.
func mixFor(mixes map[string]string, path string) string {
	best := ""
	for k := range mixes {
		if mixMatches(k, path) && len(k) > len(best) {
			best = k
		}
	}
	return mixes[best]
}

func mix(root string, p *palette, key, text string) {
	mixes := loadMixes(root)
	if _, dup := mixes[key]; dup {
		die("%s is already mixed; scrape mix %s first", key, key)
	}
	covered := 0
	for _, s := range p.Resources {
		s.walk(func(a *attr) {
			if mixMatches(key, a.Path) && a.Desc == "" {
				covered++
			}
		})
	}
	if covered == 0 {
		die("no undescribed attribute ends with %s", key)
	}
	plain(text, 40)
	if !strings.HasSuffix(text, ".") {
		die("a mix ends with a period")
	}
	verifyTicks(p, text)
	verifyRefs(p, text)
	spend(root, "mix")
	mixes[key] = text
	saveJSON(mixPath(root), mixes)
	fmt.Printf("mixed %s (colours %d attributes)\n", key, covered)
}

func lookMixes(root string) {
	mixes := loadMixes(root)
	keys := make([]string, 0, len(mixes))
	for k := range mixes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("== mixes (%d)\n", len(keys))
	for _, k := range keys {
		fmt.Printf("  %s: %s\n", k, mixes[k])
	}
}
