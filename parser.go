package coolsvg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"io"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
)

// ErrorMode controls how the parser handles unsupported SVG elements.
type ErrorMode uint8

const (
	// IgnoreErrorMode silently skips unsupported elements.
	IgnoreErrorMode ErrorMode = iota
	// WarnErrorMode logs unsupported elements.
	WarnErrorMode
	// StrictErrorMode returns an error for unsupported elements.
	StrictErrorMode
)

// ViewBox describes the SVG user-coordinate viewport.
type ViewBox struct {
	X float64
	Y float64
	W float64
	H float64
}

// SvgIcon is a parsed SVG scene.
type SvgIcon struct {
	ViewBox      ViewBox
	Titles       []string
	Descriptions []string

	root         *node
	ids          map[string]*node
	css          []cssRule
	target       Matrix
	warnings     []string
	warningSet   map[string]struct{}
	errorMode    ErrorMode
	currentColor *color.NRGBA
}

// Warnings returns a copy of non-fatal parse warnings.
func (s *SvgIcon) Warnings() []string {
	return append([]string(nil), s.warnings...)
}

// SetTarget maps the icon viewBox onto the requested destination rectangle.
// As in oksvg, the two axes are scaled independently.
func (s *SvgIcon) SetTarget(x, y, width, height float64) {
	if s.ViewBox.W <= 0 || s.ViewBox.H <= 0 || width == 0 || height == 0 {
		s.target = Matrix{}
		return
	}
	s.target = translate(x, y).
		Mul(scale(width/s.ViewBox.W, height/s.ViewBox.H)).
		Mul(translate(-s.ViewBox.X, -s.ViewBox.Y))
}

type node struct {
	tag         string
	attrs       map[string]string
	children    []*node
	parent      *node
	path        *Path
	transform   Matrix
	text        strings.Builder
	unsupported bool
}

func (n *node) attr(name string) string { return n.attrs[strings.ToLower(name)] }

func (n *node) href() string {
	return strings.TrimSpace(n.attr("href"))
}

func (n *node) localRef(attribute string) (string, bool) {
	v := strings.TrimSpace(n.attr(attribute))
	if v == "" || strings.EqualFold(v, "none") {
		return "", false
	}
	if strings.HasPrefix(v, "url(") && strings.HasSuffix(v, ")") {
		v = strings.TrimSpace(v[4 : len(v)-1])
		v = strings.Trim(v, "\"'")
	}
	if strings.HasPrefix(v, "#") && len(v) > 1 {
		return v[1:], true
	}
	return "", false
}

var supportedElements = map[string]bool{
	"svg": true, "g": true, "defs": true, "symbol": true,
	"path": true, "rect": true, "circle": true, "ellipse": true,
	"line": true, "polyline": true, "polygon": true, "use": true,
	"clippath": true, "lineargradient": true, "radialgradient": true,
	"stop": true, "title": true, "desc": true, "style": true,
	"metadata": true,
}

const svgNamespace = "http://www.w3.org/2000/svg"

const (
	maxSVGDepth    = 512
	maxSVGElements = 1_000_000
	maxSVGBytes    = 64 << 20
)

// ReadIconStream parses an SVG from stream.
func ReadIconStream(stream io.Reader, modes ...ErrorMode) (*SvgIcon, error) {
	data, err := io.ReadAll(io.LimitReader(stream, maxSVGBytes+1))
	if err != nil {
		return nil, fmt.Errorf("svg: read input: %w", err)
	}
	if len(data) > maxSVGBytes {
		return nil, fmt.Errorf("svg: input exceeds %d bytes", maxSVGBytes)
	}
	mode := IgnoreErrorMode
	if len(modes) > 0 {
		mode = modes[0]
	}
	if mode > StrictErrorMode {
		return nil, fmt.Errorf("svg: invalid error mode %d", mode)
	}
	icon := &SvgIcon{
		ids:        make(map[string]*node),
		warningSet: make(map[string]struct{}),
		target:     identityMatrix(),
		errorMode:  mode,
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	var stack []*node
	elements := 0

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("svg: decode XML: %w", err)
		}
		switch value := token.(type) {
		case xml.Directive:
			if bytes.Contains(bytes.ToUpper(value), []byte("DOCTYPE")) {
				return nil, fmt.Errorf("svg: document type declarations are not supported")
			}
		case xml.StartElement:
			elements++
			if elements > maxSVGElements {
				return nil, fmt.Errorf("svg: document contains more than %d elements", maxSVGElements)
			}
			if len(stack) >= maxSVGDepth {
				return nil, fmt.Errorf("svg: document nesting exceeds %d elements", maxSVGDepth)
			}
			tag := strings.ToLower(value.Name.Local)
			supported := supportedElements[tag] && (value.Name.Space == "" || value.Name.Space == svgNamespace)
			if !supported {
				message := fmt.Sprintf("unsupported SVG element <%s>", value.Name.Local)
				if value.Name.Space != "" && value.Name.Space != svgNamespace {
					message = fmt.Sprintf("unsupported foreign element <%s> in namespace %q", value.Name.Local, value.Name.Space)
				}
				if mode == StrictErrorMode {
					return nil, fmt.Errorf("svg: %s", message)
				}
				if mode == WarnErrorMode {
					icon.addWarning(message, true)
				}
			}
			n := &node{tag: tag, attrs: make(map[string]string), transform: identityMatrix(), unsupported: !supported}
			for _, attr := range value.Attr {
				n.attrs[strings.ToLower(attr.Name.Local)] = strings.TrimSpace(attr.Value)
			}
			if raw := n.attr("transform"); raw != "" {
				n.transform, err = parseTransform(raw)
				if err != nil {
					return nil, err
				}
			}
			if raw := n.attr("d"); tag == "path" && raw != "" {
				n.path, err = ParsePath(raw)
				if err != nil {
					return nil, err
				}
			}
			if id := n.attr("id"); id != "" {
				if _, exists := icon.ids[id]; exists {
					return nil, fmt.Errorf("svg: duplicate id %q", id)
				}
				icon.ids[id] = n
			}
			if len(stack) > 0 {
				n.parent = stack[len(stack)-1]
				n.parent.children = append(n.parent.children, n)
			} else if icon.root == nil {
				icon.root = n
			} else {
				return nil, fmt.Errorf("svg: multiple root elements")
			}
			stack = append(stack, n)
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			n := stack[len(stack)-1]
			if n.tag == "title" || n.tag == "desc" || n.tag == "style" {
				n.text.Write(value)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("svg: unexpected closing element </%s>", value.Name.Local)
			}
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n.tag != strings.ToLower(value.Name.Local) {
				return nil, fmt.Errorf("svg: closing element </%s> does not match <%s>", value.Name.Local, n.tag)
			}
			switch n.tag {
			case "title":
				icon.Titles = append(icon.Titles, strings.TrimSpace(n.text.String()))
			case "desc":
				icon.Descriptions = append(icon.Descriptions, strings.TrimSpace(n.text.String()))
			case "style":
				rules, parseErr := parseCSS(n.text.String())
				if parseErr != nil {
					if mode == StrictErrorMode {
						return nil, parseErr
					}
					message := parseErr.Error()
					icon.addWarning(message, mode == WarnErrorMode)
				} else {
					for i := range rules {
						rules[i].order = len(icon.css) + i
					}
					icon.css = append(icon.css, rules...)
				}
			}
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("svg: unclosed element <%s>", stack[len(stack)-1].tag)
	}
	if icon.root == nil || icon.root.tag != "svg" || icon.root.unsupported {
		return nil, fmt.Errorf("svg: root element must be <svg>")
	}
	viewBox, err := readViewBox(icon.root)
	if err != nil {
		return nil, err
	}
	icon.ViewBox = viewBox
	icon.target = identityMatrix()
	return icon, nil
}

const maxWarnings = 100

func (s *SvgIcon) addWarning(message string, shouldLog bool) {
	if _, exists := s.warningSet[message]; exists {
		return
	}
	if len(s.warnings) >= maxWarnings {
		return
	}
	s.warningSet[message] = struct{}{}
	s.warnings = append(s.warnings, message)
	if shouldLog {
		log.Printf("coolsvg: %s", message)
	}
}

// ReadIcon parses an SVG file.
func ReadIcon(filename string, modes ...ErrorMode) (*SvgIcon, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadIconStream(f, modes...)
}

// ReadReplacingCurrentColor parses an SVG after resolving currentColor to the
// supplied CSS color. It is provided for easy migration from oksvg.
func ReadReplacingCurrentColor(stream io.Reader, currentColor string, modes ...ErrorMode) (*SvgIcon, error) {
	var replacement *color.NRGBA
	if currentColor != "" {
		parsed, err := ParseSVGColor(currentColor)
		if err != nil || parsed == nil {
			return nil, fmt.Errorf("svg: invalid currentColor %q", currentColor)
		}
		value := color.NRGBAModel.Convert(parsed).(color.NRGBA)
		replacement = &value
	}
	icon, err := ReadIconStream(stream, modes...)
	if err != nil {
		return nil, err
	}
	icon.currentColor = replacement
	return icon, nil
}

func readViewBox(root *node) (ViewBox, error) {
	if raw := root.attr("viewbox"); raw != "" {
		values, err := parseNumberList(raw)
		if err != nil || len(values) != 4 {
			return ViewBox{}, fmt.Errorf("svg: viewBox must contain four numbers")
		}
		if values[2] <= 0 || values[3] <= 0 {
			return ViewBox{}, fmt.Errorf("svg: viewBox width and height must be positive")
		}
		return ViewBox{X: values[0], Y: values[1], W: values[2], H: values[3]}, nil
	}
	width, err := parseLength(root.attr("width"), 300)
	if err != nil {
		return ViewBox{}, fmt.Errorf("svg: width: %w", err)
	}
	height, err := parseLength(root.attr("height"), 150)
	if err != nil {
		return ViewBox{}, fmt.Errorf("svg: height: %w", err)
	}
	if root.attr("width") == "" {
		width = 300
	}
	if root.attr("height") == "" {
		height = 150
	}
	if width <= 0 || height <= 0 {
		return ViewBox{}, fmt.Errorf("svg: width and height must be positive")
	}
	return ViewBox{W: width, H: height}, nil
}

func parseLength(raw string, percentReference float64) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	units := ""
	for _, suffix := range []string{"grad", "rad", "turn", "px", "pt", "pc", "in", "cm", "mm", "q", "%"} {
		if strings.HasSuffix(strings.ToLower(raw), suffix) {
			units = suffix
			raw = strings.TrimSpace(raw[:len(raw)-len(suffix)])
			break
		}
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Errorf("invalid length %q", raw+units)
	}
	switch units {
	case "", "px":
		return v, nil
	case "%":
		return v * percentReference / 100, nil
	case "pt":
		return v * 96 / 72, nil
	case "pc":
		return v * 16, nil
	case "in":
		return v * 96, nil
	case "cm":
		return v * 96 / 2.54, nil
	case "mm":
		return v * 96 / 25.4, nil
	case "q":
		return v * 96 / 101.6, nil
	default:
		return 0, fmt.Errorf("unsupported length unit %q", units)
	}
}

func (n *node) geometry(viewport ViewBox) (*Path, error) {
	if n.path != nil {
		return n.path, nil
	}
	xLength := func(name string) (float64, error) { return parseLength(n.attr(name), viewport.W) }
	yLength := func(name string) (float64, error) { return parseLength(n.attr(name), viewport.H) }
	diagonal := math.Hypot(viewport.W, viewport.H) / math.Sqrt2
	rLength := func(name string) (float64, error) { return parseLength(n.attr(name), diagonal) }
	p := &Path{}
	switch n.tag {
	case "path":
		return nil, nil
	case "rect":
		x, err := xLength("x")
		if err != nil {
			return nil, err
		}
		y, err := yLength("y")
		if err != nil {
			return nil, err
		}
		w, err := xLength("width")
		if err != nil {
			return nil, err
		}
		h, err := yLength("height")
		if err != nil {
			return nil, err
		}
		if w < 0 || h < 0 {
			return nil, fmt.Errorf("svg: rect dimensions cannot be negative")
		}
		if w == 0 || h == 0 {
			return nil, nil
		}
		rx, err := xLength("rx")
		if err != nil {
			return nil, err
		}
		ry, err := yLength("ry")
		if err != nil {
			return nil, err
		}
		if n.attr("rx") == "" {
			rx = ry
		}
		if n.attr("ry") == "" {
			ry = rx
		}
		if rx < 0 || ry < 0 {
			return nil, fmt.Errorf("svg: rect corner radii cannot be negative")
		}
		rx, ry = math.Min(rx, w/2), math.Min(ry, h/2)
		appendRect(p, x, y, w, h, rx, ry)
	case "circle":
		cx, err := xLength("cx")
		if err != nil {
			return nil, err
		}
		cy, err := yLength("cy")
		if err != nil {
			return nil, err
		}
		r, err := rLength("r")
		if err != nil {
			return nil, err
		}
		if r < 0 {
			return nil, fmt.Errorf("svg: circle radius cannot be negative")
		}
		if r == 0 {
			return nil, nil
		}
		appendEllipse(p, cx, cy, r, r)
	case "ellipse":
		cx, err := xLength("cx")
		if err != nil {
			return nil, err
		}
		cy, err := yLength("cy")
		if err != nil {
			return nil, err
		}
		rx, err := xLength("rx")
		if err != nil {
			return nil, err
		}
		ry, err := yLength("ry")
		if err != nil {
			return nil, err
		}
		if rx < 0 || ry < 0 {
			return nil, fmt.Errorf("svg: ellipse radii cannot be negative")
		}
		if rx == 0 || ry == 0 {
			return nil, nil
		}
		appendEllipse(p, cx, cy, rx, ry)
	case "line":
		x1, err := xLength("x1")
		if err != nil {
			return nil, err
		}
		y1, err := yLength("y1")
		if err != nil {
			return nil, err
		}
		x2, err := xLength("x2")
		if err != nil {
			return nil, err
		}
		y2, err := yLength("y2")
		if err != nil {
			return nil, err
		}
		p.moveTo(Point{x1, y1})
		p.lineTo(Point{x2, y2})
	case "polyline", "polygon":
		points, err := parseNumberList(n.attr("points"))
		if err != nil {
			return nil, err
		}
		if len(points)%2 != 0 {
			return nil, fmt.Errorf("svg: points must contain coordinate pairs")
		}
		if len(points) < 4 {
			return nil, nil
		}
		p.moveTo(Point{points[0], points[1]})
		for i := 2; i < len(points); i += 2 {
			p.lineTo(Point{points[i], points[i+1]})
		}
		if n.tag == "polygon" {
			p.close()
		}
	default:
		return nil, nil
	}
	return p, nil
}

func appendRect(p *Path, x, y, width, height, rx, ry float64) {
	if rx == 0 || ry == 0 {
		p.moveTo(Point{x, y})
		p.lineTo(Point{x + width, y})
		p.lineTo(Point{x + width, y + height})
		p.lineTo(Point{x, y + height})
		p.close()
		return
	}
	k := 0.5522847498307936
	p.moveTo(Point{x + rx, y})
	p.lineTo(Point{x + width - rx, y})
	p.cubicTo(Point{x + width - rx + k*rx, y}, Point{x + width, y + ry - k*ry}, Point{x + width, y + ry})
	p.lineTo(Point{x + width, y + height - ry})
	p.cubicTo(Point{x + width, y + height - ry + k*ry}, Point{x + width - rx + k*rx, y + height}, Point{x + width - rx, y + height})
	p.lineTo(Point{x + rx, y + height})
	p.cubicTo(Point{x + rx - k*rx, y + height}, Point{x, y + height - ry + k*ry}, Point{x, y + height - ry})
	p.lineTo(Point{x, y + ry})
	p.cubicTo(Point{x, y + ry - k*ry}, Point{x + rx - k*rx, y}, Point{x + rx, y})
	p.close()
}

func appendEllipse(p *Path, cx, cy, rx, ry float64) {
	k := 0.5522847498307936
	p.moveTo(Point{cx + rx, cy})
	p.cubicTo(Point{cx + rx, cy + k*ry}, Point{cx + k*rx, cy + ry}, Point{cx, cy + ry})
	p.cubicTo(Point{cx - k*rx, cy + ry}, Point{cx - rx, cy + k*ry}, Point{cx - rx, cy})
	p.cubicTo(Point{cx - rx, cy - k*ry}, Point{cx - k*rx, cy - ry}, Point{cx, cy - ry})
	p.cubicTo(Point{cx + k*rx, cy - ry}, Point{cx + rx, cy - k*ry}, Point{cx + rx, cy})
	p.close()
}

// ParseSVGColorNum parses #rgb and #rrggbb colors. The leading # is optional.
func ParseSVGColorNum(value string) (r, g, b uint8, err error) {
	hex := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0, fmt.Errorf("svg: hexadecimal color must contain 3 or 6 digits")
	}
	parsed, parseErr := strconv.ParseUint(hex, 16, 32)
	if parseErr != nil {
		return 0, 0, 0, fmt.Errorf("svg: invalid hexadecimal color %q", value)
	}
	return uint8(parsed >> 16), uint8(parsed >> 8), uint8(parsed), nil
}
