package coolsvg

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
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
	bounds     image.Rectangle
}

func (g *gradientImage) ColorModel() color.Model { return color.NRGBAModel }
func (g *gradientImage) Bounds() image.Rectangle { return g.bounds }

func (g *gradientImage) At(x, y int) color.Color {
	return g.colorAt(x, y)
}

// RGBA64At lets image/draw avoid allocating a color interface value for every
// gradient pixel.
func (g *gradientImage) RGBA64At(x, y int) color.RGBA64 {
	return nrgbaToRGBA64(g.colorAt(x, y))
}

func (g *gradientImage) colorAt(x, y int) color.NRGBA {
	value := gradientValueAt(g.definition, g.inverse, x, y)
	result := interpolateStops(g.definition.stops, value)
	result.A = uint8(math.Round(float64(result.A) * g.opacity))
	return result
}

// gradientValueAt evaluates the shared geometry and spread rules used by both
// the sRGB and CMYK-native gradient samplers.
func gradientValueAt(definition gradientDefinition, inverse Matrix, x, y int) float64 {
	p := inverse.Apply(Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
	value := 0.0
	if definition.kind == "lineargradient" {
		x1, y1 := definition.number("x1"), definition.number("y1")
		x2, y2 := definition.number("x2"), definition.number("y2")
		dx, dy := x2-x1, y2-y1
		denominator := dx*dx + dy*dy
		if denominator > 0 {
			value = ((p.X-x1)*dx + (p.Y-y1)*dy) / denominator
		}
	} else {
		cx, cy := definition.number("cx"), definition.number("cy")
		fx, fy := definition.number("fx"), definition.number("fy")
		radius := definition.number("r")
		fr := definition.number("fr")
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
	return spreadGradient(value, definition.spread)
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
	definition = normalizeGradientDefinition(definition, r.icon.ViewBox)
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
	return &gradientImage{
		definition: definition,
		inverse:    inverse,
		opacity:    math.Max(0, math.Min(1, opacity)),
		bounds:     r.bounds,
	}, nil
}

func (r *renderer) resolveGradient(n *node, visiting map[*node]bool) (gradientDefinition, error) {
	if visiting[n] {
		return gradientDefinition{}, fmt.Errorf("svg: cyclic gradient reference %q", n.attr("id"))
	}
	if len(visiting) >= maxReferenceDepth {
		return gradientDefinition{}, fmt.Errorf("svg: gradient reference depth exceeds %d", maxReferenceDepth)
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
			definition.values[name] = strings.TrimSpace(value)
		}
	}
	if units := strings.ToLower(n.attr("gradientunits")); units == "userspaceonuse" || units == "objectboundingbox" {
		definition.units = units
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
		definition.values["x1"] = "0%"
		definition.values["y1"] = "0%"
		definition.values["x2"] = "100%"
		definition.values["y2"] = "0%"
	} else {
		definition.values["cx"] = "50%"
		definition.values["cy"] = "50%"
		definition.values["r"] = "50%"
		definition.values["fr"] = "0%"
	}
	return definition
}

func normalizeGradientDefinition(definition gradientDefinition, viewport ViewBox) gradientDefinition {
	if definition.kind == "radialgradient" {
		if definition.values["fx"] == "" {
			definition.values["fx"] = definition.values["cx"]
		}
		if definition.values["fy"] == "" {
			definition.values["fy"] = definition.values["cy"]
		}
	}
	for _, name := range []string{"x1", "y1", "x2", "y2", "cx", "cy", "r", "fx", "fy", "fr"} {
		if value := definition.values[name]; value != "" {
			definition.values[name] = normalizeGradientLength(value, definition.units, name, viewport)
		}
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
		parentStyle, err := r.definitionParentStyle(child)
		if err != nil {
			return nil, err
		}
		style, err := r.icon.computeStyle(child, parentStyle)
		if err != nil {
			return nil, err
		}
		stopColor := color.NRGBA{}
		switch style.stopColor.kind {
		case paintColor:
			stopColor = style.stopColor.color
		case paintCurrentColor:
			stopColor = style.color
			if r.icon.currentColor != nil {
				stopColor = *r.icon.currentColor
			}
		}
		stopColor.A = uint8(math.Round(float64(stopColor.A) * style.stopOpacity))
		stops = append(stops, gradientStop{offset: offset, color: stopColor})
	}
	slices.SortStableFunc(stops, func(a, b gradientStop) int {
		if a.offset < b.offset {
			return -1
		}
		if a.offset > b.offset {
			return 1
		}
		return 0
	})
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
