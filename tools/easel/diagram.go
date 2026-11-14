package main

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A diagram is drawn the way the text is painted: one box, one arrow, one frame at a time,
// on a small grid, each with a short label. There is no brush that accepts a finished
// picture, and the grid is deliberately small so a diagram can only say one thing.

type dBox struct {
	ID    string `json:"id"`
	Col   int    `json:"col"`
	Row   int    `json:"row"`
	Wide  int    `json:"wide,omitempty"` // columns spanned, default 1
	Label string `json:"label"`
	Sub   string `json:"sub,omitempty"`
	Kind  string `json:"kind,omitempty"` // "", "accent", "soft"
}

type dArrow struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Label  string `json:"label,omitempty"`
	Dashed bool   `json:"dashed,omitempty"`
}

type dFrame struct {
	ID    string `json:"id"`
	Col   int    `json:"col"`
	Row   int    `json:"row"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
	Label string `json:"label"`
}

type diagram struct {
	Name   string   `json:"name"`
	Title  string   `json:"title"`
	Seq    int      `json:"seq"`
	Boxes  []dBox   `json:"boxes"`
	Arrows []dArrow `json:"arrows"`
	Frames []dFrame `json:"frames"`
}

const (
	gridCols, gridRows, maxBoxes = 5, 4, 10
	cellW, cellH, boxW, boxH     = 256, 130, 176, 72
	padX, padTop, padBottom      = 24, 58, 20
)

var dgNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func diagramPath(root, name string) string {
	return filepath.Join(canvasDir(root), "diagrams", name+".json")
}

func diagramFile(name string) string { return "assets/diagrams/" + name + ".svg" }

func loadDiagram(root, name string) *diagram {
	raw, err := os.ReadFile(diagramPath(root, name))
	if err != nil {
		die("no diagram %q; sketch it first", name)
	}
	d := &diagram{}
	if err := json.Unmarshal(raw, d); err != nil {
		die("diagram %s: %v", name, err)
	}
	return d
}

func allDiagrams(root string) []*diagram {
	var out []*diagram
	entries, _ := os.ReadDir(filepath.Join(canvasDir(root), "diagrams"))
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".json") {
			out = append(out, loadDiagram(root, strings.TrimSuffix(en.Name(), ".json")))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (d *diagram) save(root string) { saveJSON(diagramPath(root, d.Name), d) }

func (d *diagram) box(id string) *dBox {
	for i := range d.Boxes {
		if d.Boxes[i].ID == id {
			return &d.Boxes[i]
		}
	}
	return nil
}

// label checks the few words a diagram element may carry. A word that looks like a
// resource type must be one.
func label(p *palette, text string, max int) {
	plain(text, max)
	if strings.ContainsAny(text, "`\"&") {
		die("a diagram label is plain words")
	}
	for _, w := range strings.Fields(text) {
		w = strings.Trim(w, ".,:;()")
		if strings.HasPrefix(w, "obs_") && p.Resources[w] == nil {
			die("%s is not a resource of the provider", w)
		}
	}
}

func sketch(root string, p *palette, name, title string) {
	if !dgNameRe.MatchString(name) {
		die("a diagram name is lower-case words joined by dashes")
	}
	if _, err := os.Stat(diagramPath(root, name)); err == nil {
		die("diagram %s is already sketched", name)
	}
	label(p, title, 8)
	(&diagram{Name: name, Title: title}).save(root)
	fmt.Printf("sketched %s\n", name)
}

func (d *diagram) next(prefix string) string {
	d.Seq++
	return prefix + strconv.Itoa(d.Seq)
}

func addBox(root string, p *palette, name, colS, rowS, text string, fl map[string]string) {
	d := loadDiagram(root, name)
	col, e1 := strconv.Atoi(colS)
	row, e2 := strconv.Atoi(rowS)
	wide := 1
	if fl["wide"] != "" {
		wide, _ = strconv.Atoi(fl["wide"])
	}
	if e1 != nil || e2 != nil || col < 0 || row < 0 || wide < 1 || col+wide > gridCols || row >= gridRows {
		die("the grid is %d columns by %d rows, counted from 0", gridCols, gridRows)
	}
	if len(d.Boxes) >= maxBoxes {
		die("a diagram holds at most %d boxes; draw a second diagram", maxBoxes)
	}
	for _, b := range d.Boxes {
		if b.Row == row && col < b.Col+max(b.Wide, 1) && b.Col < col+wide {
			die("that cell is taken by [%s]", b.ID)
		}
	}
	label(p, text, 4)
	if fl["sub"] != "" {
		label(p, fl["sub"], 6)
	}
	if k := fl["kind"]; k != "" && k != "accent" && k != "soft" {
		die("box kinds: accent, soft")
	}
	spend(root, "dg:"+name)
	b := dBox{ID: d.next("n"), Col: col, Row: row, Label: text, Sub: fl["sub"], Kind: fl["kind"]}
	if wide > 1 {
		b.Wide = wide
	}
	d.Boxes = append(d.Boxes, b)
	d.save(root)
	fmt.Printf("[%s] box %s\n", b.ID, text)
}

func addArrow(root string, p *palette, name, from, to, text string, dashed bool) {
	d := loadDiagram(root, name)
	if d.box(from) == nil || d.box(to) == nil || from == to {
		die("an arrow joins two different boxes that exist")
	}
	for _, a := range d.Arrows {
		if (a.From == from && a.To == to) || (a.From == to && a.To == from) {
			die("those boxes are already joined by [%s]", a.ID)
		}
	}
	if text != "" {
		label(p, text, 4)
	}
	spend(root, "dg:"+name)
	a := dArrow{ID: d.next("a"), From: from, To: to, Label: text, Dashed: dashed}
	d.Arrows = append(d.Arrows, a)
	d.save(root)
	fmt.Printf("[%s] %s -> %s\n", a.ID, from, to)
}

func addFrame(root string, p *palette, name string, nums []string, text string) {
	d := loadDiagram(root, name)
	var v [4]int
	for i, s := range nums {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			die("a frame is col row cols rows")
		}
		v[i] = n
	}
	if v[2] < 1 || v[3] < 1 || v[0]+v[2] > gridCols || v[1]+v[3] > gridRows {
		die("the frame leaves the %d by %d grid", gridCols, gridRows)
	}
	label(p, text, 4)
	spend(root, "dg:"+name)
	f := dFrame{ID: d.next("f"), Col: v[0], Row: v[1], Cols: v[2], Rows: v[3], Label: text}
	d.Frames = append(d.Frames, f)
	d.save(root)
	fmt.Printf("[%s] frame %s\n", f.ID, text)
}

func dgScrape(root, name, id string) {
	d := loadDiagram(root, name)
	spend(root, "dg:"+name)
	n := len(d.Boxes) + len(d.Arrows) + len(d.Frames)
	keepB := d.Boxes[:0]
	for _, b := range d.Boxes {
		if b.ID != id {
			keepB = append(keepB, b)
		}
	}
	d.Boxes = keepB
	keepA := d.Arrows[:0]
	for _, a := range d.Arrows {
		if a.ID != id && a.From != id && a.To != id { // an arrow goes with its box
			keepA = append(keepA, a)
		}
	}
	d.Arrows = keepA
	keepF := d.Frames[:0]
	for _, f := range d.Frames {
		if f.ID != id {
			keepF = append(keepF, f)
		}
	}
	d.Frames = keepF
	if len(d.Boxes)+len(d.Arrows)+len(d.Frames) == n {
		die("no [%s] in diagram %s", id, name)
	}
	d.save(root)
	fmt.Printf("scraped [%s]\n", id)
}

// dgLook prints the grid as text: the painter steps back from a picture too.
func dgLook(root, name string) {
	d := loadDiagram(root, name)
	fmt.Printf("== dg:%s  %q -> %s\n", d.Name, d.Title, diagramFile(d.Name))
	cols, rows := d.extent()
	for r := 0; r < rows; r++ {
		line := "  "
		for c := 0; c < cols; c++ {
			cell := "."
			for _, b := range d.Boxes {
				if b.Row == r && b.Col == c {
					cell = "[" + b.ID + " " + b.Label + "]"
				} else if b.Row == r && c > b.Col && c < b.Col+max(b.Wide, 1) {
					cell = "<" + b.ID + ">"
				}
			}
			line += fmt.Sprintf("%-26s", cell)
		}
		fmt.Println(strings.TrimRight(line, " "))
	}
	for _, f := range d.Frames {
		fmt.Printf("  frame [%s] %q cols %d-%d rows %d-%d\n", f.ID, f.Label, f.Col, f.Col+f.Cols-1, f.Row, f.Row+f.Rows-1)
	}
	for _, a := range d.Arrows {
		style := "->"
		if a.Dashed {
			style = "-->"
		}
		fmt.Printf("  [%s] %s %s %s  %s\n", a.ID, a.From, style, a.To, a.Label)
	}
}

func (d *diagram) extent() (cols, rows int) {
	for _, b := range d.Boxes {
		cols, rows = max(cols, b.Col+max(b.Wide, 1)), max(rows, b.Row+1)
	}
	for _, f := range d.Frames {
		cols, rows = max(cols, f.Col+f.Cols), max(rows, f.Row+f.Rows)
	}
	return max(cols, 1), max(rows, 1)
}

// --- SVG ----------------------------------------------------------------------------

type rect struct{ x, y, w, h float64 }

func (b dBox) rect() rect {
	w := float64(boxW + (max(b.Wide, 1)-1)*cellW)
	return rect{float64(padX + b.Col*cellW + (cellW-boxW)/2), float64(padTop + b.Row*cellH + (cellH-boxH)/2), w, boxH}
}

// edge is where the line from the centre of r toward (tx, ty) leaves r, pushed out by gap.
func (r rect) edge(tx, ty, gap float64) (float64, float64) {
	cx, cy := r.x+r.w/2, r.y+r.h/2
	dx, dy := tx-cx, ty-cy
	if dx == 0 && dy == 0 {
		return cx, cy
	}
	t := math.Inf(1)
	if dx != 0 {
		t = math.Min(t, (r.w/2)/math.Abs(dx))
	}
	if dy != 0 {
		t = math.Min(t, (r.h/2)/math.Abs(dy))
	}
	l := math.Hypot(dx, dy)
	return cx + dx*t + dx/l*gap, cy + dy*t + dy/l*gap
}

func num(f float64) string { return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64) }

func (d *diagram) svg() string {
	const ink, paper, panel, accent, frameFill, frameLine, muted = "#1f2a3d", "#ffffff", "#f1f4f9", "#2f4b7c", "#fbf8f1", "#c8b98f", "#56627a"
	cols, rows := d.extent()
	w, h := padX*2+cols*cellW, padTop+rows*cellH+padBottom
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label="%s" font-family="-apple-system, 'Segoe UI', Helvetica, Arial, sans-serif">`+"\n", w, h, w, h, html.EscapeString(d.Title))
	fmt.Fprintf(&b, `<defs><marker id="head" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" fill="%s"/></marker></defs>`+"\n", ink)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`+"\n", w, h, paper)
	fmt.Fprintf(&b, `<text x="%d" y="34" font-size="18" font-weight="600" fill="%s">%s</text>`+"\n", padX+4, ink, html.EscapeString(d.Title))
	for _, f := range d.Frames {
		x, y := padX+f.Col*cellW+5, padTop+f.Row*cellH+3
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="12" fill="%s" stroke="%s" stroke-width="1.5"/>`+"\n", x, y, f.Cols*cellW-10, f.Rows*cellH-6, frameFill, frameLine)
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="12" font-weight="600" fill="%s">%s</text>`+"\n", x+12, y+18, muted, html.EscapeString(f.Label))
	}
	for _, a := range d.Arrows {
		from, to := d.box(a.From).rect(), d.box(a.To).rect()
		x1, y1 := from.edge(to.x+to.w/2, to.y+to.h/2, 3)
		x2, y2 := to.edge(from.x+from.w/2, from.y+from.h/2, 5)
		dash := ""
		if a.Dashed {
			dash = ` stroke-dasharray="6 5"`
		}
		fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1.8"%s marker-end="url(#head)"/>`+"\n", num(x1), num(y1), num(x2), num(y2), ink, dash)
		if a.Label != "" {
			mx, my, tw := (x1+x2)/2, (y1+y2)/2, float64(len(a.Label))*6.6+12
			if y1 == y2 { // a level arrow carries its label above the line
				my -= 12
			}
			fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="18" rx="4" fill="%s"/>`+"\n", num(mx-tw/2), num(my-9), num(tw), paper)
			fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="12" text-anchor="middle" fill="%s">%s</text>`+"\n", num(mx), num(my+4), muted, html.EscapeString(a.Label))
		}
	}
	for _, bx := range d.Boxes {
		r := bx.rect()
		fill, line, text, sub, extra := panel, ink, ink, muted, ""
		switch bx.Kind {
		case "accent":
			fill, line, text, sub = accent, accent, paper, "#dbe4f3"
		case "soft":
			fill, extra = paper, ` stroke-dasharray="5 4"`
		}
		fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" rx="9" fill="%s" stroke="%s" stroke-width="1.5"%s/>`+"\n", num(r.x), num(r.y), num(r.w), num(r.h), fill, line, extra)
		cx, cy := r.x+r.w/2, r.y+r.h/2
		if bx.Sub == "" {
			fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="14" font-weight="600" text-anchor="middle" fill="%s">%s</text>`+"\n", num(cx), num(cy+5), text, html.EscapeString(bx.Label))
			continue
		}
		fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="14" font-weight="600" text-anchor="middle" fill="%s">%s</text>`+"\n", num(cx), num(cy-4), text, html.EscapeString(bx.Label))
		fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="11.5" text-anchor="middle" fill="%s">%s</text>`+"\n", num(cx), num(cy+15), sub, html.EscapeString(bx.Sub))
	}
	b.WriteString("</svg>\n")
	return b.String()
}
