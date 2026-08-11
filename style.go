package coolsvg

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/image/colornames"
)

type fillRule uint8

const (
	nonZero fillRule = iota
	evenOdd
)

type paintKind uint8

const (
	paintNone paintKind = iota
	paintColor
	paintCurrentColor
	paintReference
)

type paintSpec struct {
	kind  paintKind
	color color.NRGBA
	ref   string
}

type computedStyle struct {
	fill          paintSpec
	stroke        paintSpec
	color         color.NRGBA
	fillOpacity   float64
	strokeOpacity float64
	opacity       float64
	fillRule      fillRule
	clipRule      fillRule
	strokeWidth   float64
	strokeCap     string
	strokeJoin    string
	miterLimit    float64
	dashArray     []float64
	dashOffset    float64
	visible       bool
	display       bool
}

func defaultComputedStyle() computedStyle {
	return computedStyle{
		fill:          paintSpec{kind: paintColor, color: color.NRGBA{A: 255}},
		color:         color.NRGBA{A: 255},
		fillOpacity:   1,
		strokeOpacity: 1,
		opacity:       1,
		strokeWidth:   1,
		strokeCap:     "butt",
		strokeJoin:    "miter",
		miterLimit:    4,
		visible:       true,
		display:       true,
	}
}

type cssRule struct {
	selector     string
	declarations map[string]string
	specificity  int
	order        int
}

func parseCSS(source string) ([]cssRule, error) {
	for {
		start := strings.Index(source, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(source[start+2:], "*/")
		if end < 0 {
			return nil, fmt.Errorf("svg: unterminated CSS comment")
		}
		source = source[:start] + source[start+2+end+2:]
	}
	var rules []cssRule
	order := 0
	for strings.TrimSpace(source) != "" {
		open := strings.IndexByte(source, '{')
		if open < 0 {
			return nil, fmt.Errorf("svg: malformed CSS rule")
		}
		close := strings.IndexByte(source[open+1:], '}')
		if close < 0 {
			return nil, fmt.Errorf("svg: malformed CSS rule")
		}
		close += open + 1
		selectors := strings.Split(source[:open], ",")
		declarations, err := parseDeclarations(source[open+1 : close])
		if err != nil {
			return nil, err
		}
		for _, selector := range selectors {
			selector = strings.TrimSpace(selector)
			if selector == "" || strings.ContainsAny(selector, " >+~:[") {
				continue
			}
			specificity := 1
			if selector[0] == '#' {
				specificity = 100
			} else if selector[0] == '.' {
				specificity = 10
			}
			rules = append(rules, cssRule{selector: selector, declarations: declarations, specificity: specificity, order: order})
			order++
		}
		source = source[close+1:]
	}
	return rules, nil
}

func parseDeclarations(source string) (map[string]string, error) {
	declarations := make(map[string]string)
	for _, item := range strings.Split(source, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, value, ok := strings.Cut(item, ":")
		if !ok {
			return nil, fmt.Errorf("svg: malformed CSS declaration %q", item)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if important := strings.LastIndex(strings.ToLower(value), "!important"); important >= 0 {
			value = strings.TrimSpace(value[:important])
		}
		declarations[name] = value
	}
	return declarations, nil
}

func (r cssRule) matches(n *node) bool {
	if r.selector == "*" {
		return true
	}
	if strings.HasPrefix(r.selector, "#") {
		return n.attr("id") == r.selector[1:]
	}
	if strings.HasPrefix(r.selector, ".") {
		wanted := r.selector[1:]
		for _, class := range strings.Fields(n.attr("class")) {
			if class == wanted {
				return true
			}
		}
		return false
	}
	return strings.EqualFold(n.tag, r.selector)
}

var presentationProperties = []string{
	"color", "fill", "fill-opacity", "fill-rule", "stroke", "stroke-opacity",
	"stroke-width", "stroke-linecap", "stroke-linejoin", "stroke-miterlimit",
	"stroke-dasharray", "stroke-dashoffset", "clip-rule", "opacity", "display",
	"visibility",
}

func (s *SvgIcon) computeStyle(n *node, parent computedStyle) (computedStyle, error) {
	style := parent
	// These properties apply to the element as a compositing operation and are
	// not inherited by its children.
	style.opacity = 1
	style.display = true

	var matching []cssRule
	for _, rule := range s.css {
		if rule.matches(n) {
			matching = append(matching, rule)
		}
	}
	sort.SliceStable(matching, func(i, j int) bool {
		if matching[i].specificity == matching[j].specificity {
			return matching[i].order < matching[j].order
		}
		return matching[i].specificity < matching[j].specificity
	})
	for _, rule := range matching {
		if err := applyDeclarations(&style, parent, rule.declarations); err != nil {
			return style, err
		}
	}
	for _, property := range presentationProperties {
		if value := n.attr(property); value != "" {
			if err := applyDeclaration(&style, parent, property, value); err != nil {
				return style, err
			}
		}
	}
	if raw := n.attr("style"); raw != "" {
		declarations, err := parseDeclarations(raw)
		if err != nil {
			return style, err
		}
		if err := applyDeclarations(&style, parent, declarations); err != nil {
			return style, err
		}
	}
	return style, nil
}

func applyDeclarations(style *computedStyle, parent computedStyle, declarations map[string]string) error {
	for name, value := range declarations {
		if err := applyDeclaration(style, parent, name, value); err != nil {
			return err
		}
	}
	return nil
}

func applyDeclaration(style *computedStyle, parent computedStyle, name, value string) error {
	name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
	if strings.EqualFold(value, "inherit") {
		switch name {
		case "fill":
			style.fill = parent.fill
		case "stroke":
			style.stroke = parent.stroke
		case "color":
			style.color = parent.color
		case "fill-opacity":
			style.fillOpacity = parent.fillOpacity
		case "stroke-opacity":
			style.strokeOpacity = parent.strokeOpacity
		case "opacity":
			style.opacity = parent.opacity
		case "fill-rule":
			style.fillRule = parent.fillRule
		case "clip-rule":
			style.clipRule = parent.clipRule
		case "visibility":
			style.visible = parent.visible
		case "display":
			style.display = parent.display
		}
		return nil
	}
	switch name {
	case "fill", "stroke":
		paint, err := parsePaint(value)
		if err != nil {
			return fmt.Errorf("svg: %s: %w", name, err)
		}
		if name == "fill" {
			style.fill = paint
		} else {
			style.stroke = paint
		}
	case "color":
		parsed, err := ParseSVGColor(value)
		if err != nil || parsed == nil {
			return fmt.Errorf("svg: color: invalid color %q", value)
		}
		style.color = color.NRGBAModel.Convert(parsed).(color.NRGBA)
	case "fill-opacity", "stroke-opacity", "opacity":
		opacity, err := parseFraction(value)
		if err != nil {
			return fmt.Errorf("svg: %s: %w", name, err)
		}
		switch name {
		case "fill-opacity":
			style.fillOpacity = opacity
		case "stroke-opacity":
			style.strokeOpacity = opacity
		case "opacity":
			style.opacity = opacity
		}
	case "fill-rule", "clip-rule":
		var rule fillRule
		switch strings.ToLower(value) {
		case "nonzero":
			rule = nonZero
		case "evenodd":
			rule = evenOdd
		default:
			return fmt.Errorf("svg: %s: invalid rule %q", name, value)
		}
		if name == "fill-rule" {
			style.fillRule = rule
		} else {
			style.clipRule = rule
		}
	case "stroke-width":
		width, err := parseLength(value, 1)
		if err != nil || width < 0 {
			return fmt.Errorf("svg: stroke-width: invalid width %q", value)
		}
		style.strokeWidth = width
	case "stroke-linecap":
		switch strings.ToLower(value) {
		case "butt", "round", "square":
			style.strokeCap = strings.ToLower(value)
		default:
			return fmt.Errorf("svg: stroke-linecap: invalid value %q", value)
		}
	case "stroke-linejoin":
		switch strings.ToLower(value) {
		case "miter", "miter-clip", "round", "bevel", "arcs":
			style.strokeJoin = strings.ToLower(value)
		default:
			return fmt.Errorf("svg: stroke-linejoin: invalid value %q", value)
		}
	case "stroke-miterlimit":
		limit, err := strconv.ParseFloat(value, 64)
		if err != nil || limit < 1 {
			return fmt.Errorf("svg: stroke-miterlimit: invalid value %q", value)
		}
		style.miterLimit = limit
	case "stroke-dasharray":
		if strings.EqualFold(value, "none") {
			style.dashArray = nil
			break
		}
		values, err := parseNumberList(value)
		if err != nil {
			return fmt.Errorf("svg: stroke-dasharray: %w", err)
		}
		total := 0.0
		for _, dash := range values {
			if dash < 0 {
				return fmt.Errorf("svg: stroke-dasharray values cannot be negative")
			}
			total += dash
		}
		if total == 0 {
			values = nil
		} else if len(values)%2 == 1 {
			values = append(values, values...)
		}
		style.dashArray = values
	case "stroke-dashoffset":
		offset, err := parseLength(value, 1)
		if err != nil {
			return fmt.Errorf("svg: stroke-dashoffset: %w", err)
		}
		style.dashOffset = offset
	case "display":
		style.display = !strings.EqualFold(value, "none")
	case "visibility":
		style.visible = !strings.EqualFold(value, "hidden") && !strings.EqualFold(value, "collapse")
	}
	return nil
}

func parsePaint(value string) (paintSpec, error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "none") || value == "" {
		return paintSpec{}, nil
	}
	if strings.EqualFold(value, "currentcolor") {
		return paintSpec{kind: paintCurrentColor}, nil
	}
	if strings.HasPrefix(strings.ToLower(value), "url(") {
		end := strings.IndexByte(value, ')')
		if end < 0 {
			return paintSpec{}, fmt.Errorf("invalid paint server %q", value)
		}
		ref := strings.Trim(strings.TrimSpace(value[4:end]), "\"'")
		if !strings.HasPrefix(ref, "#") || len(ref) == 1 {
			return paintSpec{}, fmt.Errorf("external paint servers are not supported")
		}
		return paintSpec{kind: paintReference, ref: ref[1:]}, nil
	}
	parsed, err := ParseSVGColor(value)
	if err != nil || parsed == nil {
		return paintSpec{}, fmt.Errorf("invalid color %q", value)
	}
	return paintSpec{kind: paintColor, color: color.NRGBAModel.Convert(parsed).(color.NRGBA)}, nil
}

func parseFraction(value string) (float64, error) {
	value = strings.TrimSpace(value)
	percent := strings.HasSuffix(value, "%")
	if percent {
		value = strings.TrimSpace(strings.TrimSuffix(value, "%"))
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("invalid fraction %q", value)
	}
	if percent {
		v /= 100
	}
	return math.Max(0, math.Min(1, v)), nil
}

// ParseSVGColor parses CSS named, hexadecimal, rgb[a], and hsl[a] colors.
// The special value "none" returns a nil color and no error.
func ParseSVGColor(value string) (color.Color, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if lower == "" || lower == "none" {
		return nil, nil
	}
	if lower == "transparent" {
		return color.NRGBA{}, nil
	}
	if lower == "rebeccapurple" {
		return color.NRGBA{R: 102, G: 51, B: 153, A: 255}, nil
	}
	if named, ok := colornames.Map[lower]; ok {
		return color.NRGBAModel.Convert(named), nil
	}
	if strings.HasPrefix(lower, "#") {
		hex := lower[1:]
		if len(hex) == 3 || len(hex) == 4 {
			var expanded strings.Builder
			for _, c := range hex {
				expanded.WriteRune(c)
				expanded.WriteRune(c)
			}
			hex = expanded.String()
		}
		if len(hex) != 6 && len(hex) != 8 {
			return nil, fmt.Errorf("invalid hexadecimal color %q", value)
		}
		n, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid hexadecimal color %q", value)
		}
		if len(hex) == 6 {
			return color.NRGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}, nil
		}
		return color.NRGBA{R: uint8(n >> 24), G: uint8(n >> 16), B: uint8(n >> 8), A: uint8(n)}, nil
	}
	name, args, ok := functionalColor(lower)
	if !ok {
		return nil, fmt.Errorf("invalid SVG color %q", value)
	}
	switch name {
	case "rgb", "rgba":
		if len(args) != 3 && len(args) != 4 {
			return nil, fmt.Errorf("invalid %s color %q", name, value)
		}
		channels := [4]uint8{0, 0, 0, 255}
		for i := 0; i < 3; i++ {
			channel, err := parseColorChannel(args[i])
			if err != nil {
				return nil, err
			}
			channels[i] = channel
		}
		if len(args) == 4 {
			alpha, err := parseAlphaChannel(args[3])
			if err != nil {
				return nil, err
			}
			channels[3] = alpha
		}
		return color.NRGBA{R: channels[0], G: channels[1], B: channels[2], A: channels[3]}, nil
	case "hsl", "hsla":
		if len(args) != 3 && len(args) != 4 {
			return nil, fmt.Errorf("invalid %s color %q", name, value)
		}
		h, err := strconv.ParseFloat(strings.TrimSuffix(args[0], "deg"), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid hue %q", args[0])
		}
		s, err := requiredPercent(args[1])
		if err != nil {
			return nil, err
		}
		l, err := requiredPercent(args[2])
		if err != nil {
			return nil, err
		}
		r, g, b := hslToRGB(h, s, l)
		a := uint8(255)
		if len(args) == 4 {
			a, err = parseAlphaChannel(args[3])
			if err != nil {
				return nil, err
			}
		}
		return color.NRGBA{R: r, G: g, B: b, A: a}, nil
	default:
		return nil, fmt.Errorf("invalid SVG color %q", value)
	}
}

func functionalColor(value string) (string, []string, bool) {
	open := strings.IndexByte(value, '(')
	if open <= 0 || !strings.HasSuffix(value, ")") {
		return "", nil, false
	}
	name := strings.TrimSpace(value[:open])
	body := strings.TrimSpace(value[open+1 : len(value)-1])
	var args []string
	if strings.Contains(body, ",") {
		for _, arg := range strings.Split(body, ",") {
			args = append(args, strings.TrimSpace(arg))
		}
	} else {
		body = strings.ReplaceAll(body, "/", " / ")
		fields := strings.Fields(body)
		for _, field := range fields {
			if field != "/" {
				args = append(args, field)
			}
		}
	}
	return name, args, true
}

func parseColorChannel(value string) (uint8, error) {
	percent := strings.HasSuffix(value, "%")
	v, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid color channel %q", value)
	}
	if percent {
		v *= 2.55
	}
	return uint8(math.Round(math.Max(0, math.Min(255, v)))), nil
}

func parseAlphaChannel(value string) (uint8, error) {
	v, err := parseFraction(value)
	if err != nil {
		return 0, err
	}
	return uint8(math.Round(v * 255)), nil
}

func requiredPercent(value string) (float64, error) {
	if !strings.HasSuffix(value, "%") {
		return 0, fmt.Errorf("expected percentage, got %q", value)
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid percentage %q", value)
	}
	return math.Max(0, math.Min(1, v/100)), nil
}

func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return uint8(math.Round((r + m) * 255)), uint8(math.Round((g + m) * 255)), uint8(math.Round((b + m) * 255))
}
