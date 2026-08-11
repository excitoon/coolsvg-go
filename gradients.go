package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"
)

type gradientStop struct {
	offset float64
	color  color.NRGBA
}

type gradientDefinition struct {
	kind      string
	units     string
	spread    string
	transform Matrix
	values    map[string]string
	stops     []gradientStop
}

type gradientImage struct {
	definition gradientDefinition
	inverse    Matrix
	opacity    float64
}

func (g *gradientImage) ColorModel() color.Model { return color.NRGBAModel }
func (g *gradientImage) Bounds() image.Rectangle { return image.Rect(-1<<30, -1<<30, 1<<30, 1<<30) }

func (g *gradientImage) At(x, y int) color.Color {
	p := g.inverse.Apply(Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
	value := 0.0
	if g.definition.kind == "lineargradient" {
		x1, y1 := g.definition.number("x1"), g.definition.number("y1")
		x2, y2 := g.definition.number("x2"), g.definition.number("y2")
		dx, dy := x2-x1, y2-y1
		denominator := dx*dx + dy*dy
		if denominator > 0 {
			value = ((p.X-x1)*dx + (p.Y-y1)*dy) / denominator
		}
	} else {
		cx, cy := g.definition.number("cx"), g.definition.number("cy")
		fx, fy := g.definition.number("fx"), g.definition.number("fy")
		radius := g.definition.number("r")
		fr := g.definition.number("fr")
		dx, dy := p.X-fx, p.Y-fy
		a := dx*dx + dy*dy
		b := 2 * (dx*(fx-cx) + dy*(fy-cy))
		c := (fx-cx)*(fx-cx) + (fy-cy)*(fy-cy) - radius*radius
		discriminant := b*b - 4*a*c
		if a > 0 && discriminant >= 0 {
			u := (-b + math.Sqrt(discriminant)) / (2 * a)
			if u <= 0 {
				u = (-b - math.Sqrt(discriminant)) / (2 * a)
			}
			if u > 0 {
				distance := math.Sqrt(a)
				outer := distance * u
				value = (distance - fr) / math.Max(outer-fr, 1e-15)
			}
		}
	}
	value = spreadGradient(value, g.definition.spread)
	result := interpolateStops(g.definition.stops, value)
	result.A = uint8(math.Round(float64(result.A) * g.opacity))
	return result
}

func (g gradientDefinition) number(name string) float64 {
	value, _ := parseLength(g.values[name], 1)
	return value
}

func (r *renderer) gradientSource(ref string, opacity float64, ctm Matrix, bounds floatBounds) (image.Image, error) {
	n := r.icon.ids[ref]
	if n == nil || n.tag != "lineargradient" && n.tag != "radialgradient" {
		return nil, nil
	}
	definition, err := r.resolveGradient(n, make(map[*node]bool))
	if err != nil {
		return nil, err
	}
	if len(definition.stops) == 0 {
		return nil, nil
	}
	matrix := ctm
	if definition.units == "objectboundingbox" {
		if !bounds.valid || bounds.width() == 0 || bounds.height() == 0 {
			return nil, nil
		}
		matrix = matrix.Mul(translate(bounds.min.X, bounds.min.Y)).Mul(scale(bounds.width(), bounds.height()))
	}
	matrix = matrix.Mul(definition.transform)
	inverse, ok := matrix.inverse()
	if !ok {
		return nil, nil
	}
	return &gradientImage{definition: definition, inverse: inverse, opacity: math.Max(0, math.Min(1, opacity))}, nil
}

func (r *renderer) resolveGradient(n *node, visiting map[*node]bool) (gradientDefinition, error) {
	if visiting[n] {
		return gradientDefinition{}, fmt.Errorf("svg: cyclic gradient reference %q", n.attr("id"))
	}
	visiting[n] = true
	defer delete(visiting, n)

	definition := defaultGradient(n.tag)
	if ref, ok := referenceID(n.href()); ok {
		base := r.icon.ids[ref]
		if base != nil && (base.tag == "lineargradient" || base.tag == "radialgradient") {
			var err error
			definition, err = r.resolveGradient(base, visiting)
			if err != nil {
				return gradientDefinition{}, err
			}
			definition.kind = n.tag
		}
	}
	for _, name := range []string{"x1", "y1", "x2", "y2", "cx", "cy", "r", "fx", "fy", "fr"} {
		if value := n.attr(name); value != "" {
			definition.values[name] = normalizeGradientLength(value, definition.units, name, r.icon.ViewBox)
		}
	}
	if units := strings.ToLower(n.attr("gradientunits")); units == "userspaceonuse" || units == "objectboundingbox" {
		definition.units = units
		if units == "userspaceonuse" {
			if _, inherited := referenceID(n.href()); !inherited {
				if n.tag == "lineargradient" {
					definition.values["x1"], definition.values["y1"] = "0", "0"
					definition.values["x2"], definition.values["y2"] = fmt.Sprintf("%.17g", r.icon.ViewBox.W), "0"
				} else {
					definition.values["cx"] = fmt.Sprintf("%.17g", r.icon.ViewBox.W/2)
					definition.values["cy"] = fmt.Sprintf("%.17g", r.icon.ViewBox.H/2)
					definition.values["r"] = fmt.Sprintf("%.17g", math.Hypot(r.icon.ViewBox.W, r.icon.ViewBox.H)/(2*math.Sqrt2))
					definition.values["fx"], definition.values["fy"] = definition.values["cx"], definition.values["cy"]
					definition.values["fr"] = "0"
				}
			}
		}
		// Re-normalize this node's coordinates after reading its units.
		for _, name := range []string{"x1", "y1", "x2", "y2", "cx", "cy", "r", "fx", "fy", "fr"} {
			if value := n.attr(name); value != "" {
				definition.values[name] = normalizeGradientLength(value, definition.units, name, r.icon.ViewBox)
			}
		}
	}
	if spread := strings.ToLower(n.attr("spreadmethod")); spread == "pad" || spread == "repeat" || spread == "reflect" {
		definition.spread = spread
	}
	if raw := n.attr("gradienttransform"); raw != "" {
		transform, err := parseTransform(raw)
		if err != nil {
			return gradientDefinition{}, err
		}
		definition.transform = transform
	}
	if stops, err := r.readGradientStops(n); err != nil {
		return gradientDefinition{}, err
	} else if len(stops) > 0 {
		definition.stops = stops
	}
	if definition.kind == "radialgradient" {
		if n.attr("fx") == "" && definition.values["fx"] == "" {
			definition.values["fx"] = definition.values["cx"]
		}
		if n.attr("fy") == "" && definition.values["fy"] == "" {
			definition.values["fy"] = definition.values["cy"]
		}
	}
	return definition, nil
}

func defaultGradient(kind string) gradientDefinition {
	definition := gradientDefinition{
		kind:      kind,
		units:     "objectboundingbox",
		spread:    "pad",
		transform: identityMatrix(),
		values:    make(map[string]string),
	}
	if kind == "lineargradient" {
		definition.values["x1"] = "0"
		definition.values["y1"] = "0"
		definition.values["x2"] = "1"
		definition.values["y2"] = "0"
	} else {
		definition.values["cx"] = "0.5"
		definition.values["cy"] = "0.5"
		definition.values["r"] = "0.5"
		definition.values["fx"] = "0.5"
		definition.values["fy"] = "0.5"
		definition.values["fr"] = "0"
	}
	return definition
}

func normalizeGradientLength(value, units, name string, viewport ViewBox) string {
	value = strings.TrimSpace(value)
	if !strings.HasSuffix(value, "%") {
		return value
	}
	reference := 1.0
	if units == "userspaceonuse" {
		switch name {
		case "x1", "x2", "cx", "fx":
			reference = viewport.W
		case "y1", "y2", "cy", "fy":
			reference = viewport.H
		case "r", "fr":
			reference = math.Hypot(viewport.W, viewport.H) / math.Sqrt2
		}
	}
	parsed, err := parseLength(value, reference)
	if err != nil {
		return "0"
	}
	return fmt.Sprintf("%.17g", parsed)
}

func (r *renderer) readGradientStops(n *node) ([]gradientStop, error) {
	var stops []gradientStop
	lastOffset := 0.0
	for _, child := range n.children {
		if child.tag != "stop" {
			continue
		}
		properties := make(declarations)
		if raw := child.attr("style"); raw != "" {
			parsed, err := parseDeclarations(raw)
			if err != nil {
				return nil, err
			}
			for key, value := range parsed {
				properties[key] = value
			}
		}
		for _, name := range []string{"stop-color", "stop-opacity"} {
			if value := child.attr(name); value != "" {
				properties[name] = declaration{value: value}
			}
		}
		offset := 0.0
		if rawOffset := child.attr("offset"); rawOffset != "" {
			var err error
			offset, err = parseFraction(rawOffset)
			if err != nil {
				return nil, fmt.Errorf("svg: gradient stop offset: %w", err)
			}
		}
		if offset < lastOffset {
			offset = lastOffset
		}
		lastOffset = offset
		stopColor := color.NRGBA{A: 255}
		if property := properties["stop-color"]; property.value != "" && !strings.EqualFold(property.value, "currentcolor") {
			value := property.value
			parsed, err := ParseSVGColor(value)
			if err != nil || parsed == nil {
				return nil, fmt.Errorf("svg: invalid gradient stop color %q", value)
			}
			stopColor = color.NRGBAModel.Convert(parsed).(color.NRGBA)
		}
		if property := properties["stop-opacity"]; property.value != "" {
			value := property.value
			opacity, err := parseFraction(value)
			if err != nil {
				return nil, err
			}
			stopColor.A = uint8(math.Round(float64(stopColor.A) * opacity))
		}
		stops = append(stops, gradientStop{offset: offset, color: stopColor})
	}
	sort.SliceStable(stops, func(i, j int) bool { return stops[i].offset < stops[j].offset })
	return stops, nil
}

func spreadGradient(value float64, spread string) float64 {
	switch spread {
	case "repeat":
		value -= math.Floor(value)
	case "reflect":
		value = math.Mod(value, 2)
		if value < 0 {
			value += 2
		}
		if value > 1 {
			value = 2 - value
		}
	default:
		value = math.Max(0, math.Min(1, value))
	}
	return value
}

func interpolateStops(stops []gradientStop, value float64) color.NRGBA {
	if len(stops) == 0 {
		return color.NRGBA{}
	}
	if value <= stops[0].offset {
		return stops[0].color
	}
	for i := 1; i < len(stops); i++ {
		if value <= stops[i].offset {
			span := stops[i].offset - stops[i-1].offset
			if span <= 0 {
				return stops[i].color
			}
			t := (value - stops[i-1].offset) / span
			return mixColor(stops[i-1].color, stops[i].color, t)
		}
	}
	return stops[len(stops)-1].color
}

func mixColor(a, b color.NRGBA, t float64) color.NRGBA {
	mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*(1-t) + float64(y)*t)) }
	return color.NRGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: mix(a.A, b.A)}
}
