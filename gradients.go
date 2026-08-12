package coolsvg

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"
)

type gradientStop struct {
	offset float64
	color  color.NRGBA
	alpha  float64
}

type gradientDefinition struct {
	kind      string
	units     string
	spread    string
	transform Matrix
	values    map[string]string
	stops     []gradientStop
	geometry  gradientGeometry
}

type gradientGeometry struct {
	x1 float64
	y1 float64
	x2 float64
	y2 float64
	cx float64
	cy float64
	fx float64
	fy float64
	r  float64
	fr float64
}

// gradientValueAt evaluates the shared geometry and spread rules used by both
// the sRGB and CMYK-native gradient samplers.
func gradientValueAt(definition gradientDefinition, inverse Matrix, x, y int) float64 {
	p := inverse.Apply(Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
	value := 0.0
	geometry := definition.geometry
	if definition.kind == "lineargradient" {
		dx, dy := geometry.x2-geometry.x1, geometry.y2-geometry.y1
		denominator := dx*dx + dy*dy
		if denominator > 0 {
			value = ((p.X-geometry.x1)*dx + (p.Y-geometry.y1)*dy) / denominator
		}
	} else {
		dx, dy := p.X-geometry.fx, p.Y-geometry.fy
		a := dx*dx + dy*dy
		b := 2 * (dx*(geometry.fx-geometry.cx) + dy*(geometry.fy-geometry.cy))
		c := (geometry.fx-geometry.cx)*(geometry.fx-geometry.cx) + (geometry.fy-geometry.cy)*(geometry.fy-geometry.cy) - geometry.r*geometry.r
		discriminant := b*b - 4*a*c
		if a > 0 && discriminant >= 0 {
			u := (-b + math.Sqrt(discriminant)) / (2 * a)
			if u <= 0 {
				u = (-b - math.Sqrt(discriminant)) / (2 * a)
			}
			if u > 0 {
				distance := math.Sqrt(a)
				outer := distance * u
				value = (distance - geometry.fr) / math.Max(outer-geometry.fr, 1e-15)
			}
		}
	}
	return spreadGradient(value, definition.spread)
}

func (g gradientDefinition) number(name string) float64 {
	value, _ := parseLength(g.values[name], 1)
	return value
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
	definition.geometry = gradientGeometry{
		x1: definition.number("x1"), y1: definition.number("y1"),
		x2: definition.number("x2"), y2: definition.number("y2"),
		cx: definition.number("cx"), cy: definition.number("cy"),
		fx: definition.number("fx"), fy: definition.number("fy"),
		r: definition.number("r"), fr: definition.number("fr"),
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
		alpha := float64(stopColor.A) / 255 * style.stopOpacity
		stopColor.A = uint8(math.Round(alpha * 255))
		stops = append(stops, gradientStop{offset: offset, color: stopColor, alpha: alpha})
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
