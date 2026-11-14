// easel is the only way documentation text reaches docs/ and the generated examples.
//
// Nothing here opens a file for free editing. A page is a canvas: an ordered list of
// small strokes, each laid with a brush that has rules. The friction is deliberate:
//
//   - A stroke is one sentence (or one heading, bullet, command). There is no brush
//     that takes a paragraph, and none that replaces text in place: a stroke that is
//     wrong is scraped off and painted again.
//   - The painter must step back. `look` shows the canvas and wets it for four strokes;
//     after that the brush is refused until the canvas has been looked at again.
//   - Code is pigment, not paint. A backticked name must exist in the provider schema
//     or verbatim in the shipped source; an import form must be quoted from the source;
//     the schema section is laid from the provider itself; HCL examples are composed
//     attribute by attribute against the schema and proved with `terraform validate`.
//   - docs/ is rendered, never written. `verify` fails when a file differs from its
//     canvas, so a hand edit does not survive.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func usage() {
	fmt.Print(`easel: paint the provider documentation

  easel palette [filter]                     what there is to paint with (schema, importability)
  easel stretch <page> [--title T] [--subcategory S]
                                             new canvas: index | resources/<name> | guides/<slug>
  easel look <page|ex:name|mix> [--all]          step back; wets the canvas for 4 strokes
  easel stroke <page> <brush> [text] [--after ID | --first] [--level N] [--new]
      brushes: line bullet step note warn heading row shell literal example schema roster picture
  easel glaze <page> <attr.path> <text>      describe one attribute of the page's schema
  easel mix <attr.suffix> <text>             describe every undescribed attribute ending in that path
  easel scrape <page|ex:name|mix> <id>       remove one stroke (there is no edit)
  easel block <ex> <kind> [labels...]        example: terraform|provider|resource|locals|variable|output
  easel set <ex> <block> <path> <expr>       example: one attribute, checked against the schema
  easel remark <ex> <block> <text>           example: one comment line above a block
  easel sketch <diagram> <title>             new diagram on a 5 by 4 grid
  easel box <dg> <col> <row> <label> [--sub S] [--kind accent|soft] [--wide N]
  easel arrow <dg> <from> <to> [label] [--dashed]
  easel frame <dg> <col> <row> <cols> <rows> <label>
                                             look and scrape take dg:<diagram>; the picture brush places one
  easel critique [page]                      what is still missing
  easel render                               write docs/ and examples/ from the canvases
  easel verify                               fail if docs/ or examples/ differ from the canvases
  easel proof                                terraform fmt + validate every composed example
`)
}

func repoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			die("run inside the repository")
		}
		dir = up
	}
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "easel: "+format+"\n", a...)
	os.Exit(1)
}

// flags pulls --name value pairs (and bare --switches listed in bools) out of args.
func flags(args []string, bools ...string) ([]string, map[string]string) {
	isBool := map[string]bool{}
	for _, b := range bools {
		isBool[b] = true
	}
	var rest []string
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if name, ok := strings.CutPrefix(a, "--"); ok && name != "" {
			if isBool[name] {
				out[name] = "true"
				continue
			}
			if i+1 >= len(args) {
				die("--%s needs a value", name)
			}
			out[name] = args[i+1]
			i++
			continue
		}
		rest = append(rest, a)
	}
	return rest, out
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	root := repoRoot()
	cmd, args := os.Args[1], os.Args[2:]
	need := func(n int, what string) {
		if len(args) < n {
			die("usage: easel %s %s", cmd, what)
		}
	}
	switch cmd {
	case "help", "-h", "--help":
		usage()
	case "palette":
		p := mustPalette(root)
		f := ""
		if len(args) > 0 {
			f = args[0]
		}
		p.print(f)
	case "stretch":
		rest, fl := flags(args)
		args = rest
		need(1, "<page> [--title T] [--subcategory S]")
		stretch(root, mustPalette(root), args[0], fl["title"], fl["subcategory"])
	case "look":
		rest, fl := flags(args, "all")
		args = rest
		need(1, "<page|ex:name> [--all]")
		look(root, args[0], fl["all"] != "")
	case "stroke":
		rest, fl := flags(args, "first", "new")
		args = rest
		need(2, "<page> <brush> [text]")
		text := ""
		if len(args) > 2 {
			text = args[2]
		}
		if len(args) > 3 {
			die("a stroke takes one text argument; quote it")
		}
		stroke(root, mustPalette(root), args[0], args[1], text, fl)
	case "glaze":
		need(3, "<page> <attr.path> <text>")
		glaze(root, mustPalette(root), args[0], args[1], args[2])
	case "mix":
		need(2, "<attr.suffix> <text>")
		mix(root, mustPalette(root), args[0], args[1])
	case "scrape":
		need(2, "<page|ex:name> <id>")
		scrape(root, args[0], args[1])
	case "block":
		need(2, "<example> <kind> [labels...]")
		exBlock(root, mustPalette(root), args[0], args[1], args[2:])
	case "set":
		need(4, "<example> <block> <path> <expr>")
		exSet(root, mustPalette(root), args[0], args[1], args[2], args[3])
	case "remark":
		need(3, "<example> <block> <text>")
		exRemark(root, args[0], args[1], args[2])
	case "sketch":
		need(2, "<diagram> <title>")
		sketch(root, mustPalette(root), args[0], args[1])
	case "box":
		rest, fl := flags(args)
		args = rest
		need(4, "<diagram> <col> <row> <label> [--sub S] [--kind accent|soft] [--wide N]")
		addBox(root, mustPalette(root), args[0], args[1], args[2], args[3], fl)
	case "arrow":
		rest, fl := flags(args, "dashed")
		args = rest
		need(3, "<diagram> <from> <to> [label] [--dashed]")
		text := ""
		if len(args) > 3 {
			text = args[3]
		}
		addArrow(root, mustPalette(root), args[0], args[1], args[2], text, fl["dashed"] != "")
	case "frame":
		need(6, "<diagram> <col> <row> <cols> <rows> <label>")
		addFrame(root, mustPalette(root), args[0], args[1:5], args[5])
	case "critique":
		only := ""
		if len(args) > 0 {
			only = args[0]
		}
		if n := critique(root, mustPalette(root), only, true); n > 0 {
			os.Exit(1)
		}
	case "render":
		render(root, mustPalette(root), true)
	case "verify":
		if n := render(root, mustPalette(root), false); n > 0 {
			die("%d file(s) differ from their canvas: they were edited by hand, or `easel render` was not run", n)
		}
		fmt.Println("docs and examples match their canvases")
	case "proof":
		proof(root)
	default:
		usage()
		os.Exit(2)
	}
}

func mustPalette(root string) *palette {
	p, err := loadPalette(root)
	if err != nil {
		die("%v", err)
	}
	return p
}
