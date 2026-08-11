package coolsvg

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Point is a point in SVG user coordinates.
type Point struct {
	X float64
	Y float64
}

// Matrix is an SVG affine transformation matrix. It uses the SVG matrix(a b c
// d e f) convention and column vectors.
type Matrix struct {
	A float64
	B float64
	C float64
	D float64
	E float64
	F float64
}

// Identity is the identity transformation. Internal rendering uses an
// immutable value so reassignment by a caller cannot affect parsed icons.
var Identity = identityMatrix()

func identityMatrix() Matrix { return Matrix{A: 1, D: 1} }

// Mul returns m*n: n is applied first, followed by m.
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		A: m.A*n.A + m.C*n.B,
		B: m.B*n.A + m.D*n.B,
		C: m.A*n.C + m.C*n.D,
		D: m.B*n.C + m.D*n.D,
		E: m.A*n.E + m.C*n.F + m.E,
		F: m.B*n.E + m.D*n.F + m.F,
	}
}

// Apply transforms a point.
func (m Matrix) Apply(p Point) Point {
	return Point{
		X: m.A*p.X + m.C*p.Y + m.E,
		Y: m.B*p.X + m.D*p.Y + m.F,
	}
}

func (m Matrix) inverse() (Matrix, bool) {
	det := m.A*m.D - m.B*m.C
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		return Matrix{}, false
	}
	result := Matrix{
		A: m.D / det,
		B: -m.B / det,
		C: -m.C / det,
		D: m.A / det,
		E: (m.C*m.F - m.D*m.E) / det,
		F: (m.B*m.E - m.A*m.F) / det,
	}
	if !result.finite() {
		return Matrix{}, false
	}
	return result, true
}

func (m Matrix) finite() bool {
	for _, value := range [...]float64{m.A, m.B, m.C, m.D, m.E, m.F} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func translate(x, y float64) Matrix { return Matrix{A: 1, D: 1, E: x, F: y} }
func scale(x, y float64) Matrix     { return Matrix{A: x, D: y} }

func rotate(radians float64) Matrix {
	s, c := math.Sincos(radians)
	return Matrix{A: c, B: s, C: -s, D: c}
}

func parseTransform(value string) (Matrix, error) {
	m := identityMatrix()
	for pos := 0; ; {
		for pos < len(value) && (unicode.IsSpace(rune(value[pos])) || value[pos] == ',') {
			pos++
		}
		if pos == len(value) {
			return m, nil
		}
		start := pos
		for pos < len(value) && unicode.IsLetter(rune(value[pos])) {
			pos++
		}
		if start == pos {
			return Matrix{}, fmt.Errorf("svg: invalid transform %q", value)
		}
		name := strings.ToLower(value[start:pos])
		for pos < len(value) && unicode.IsSpace(rune(value[pos])) {
			pos++
		}
		if pos >= len(value) || value[pos] != '(' {
			return Matrix{}, fmt.Errorf("svg: missing '(' in transform %q", value)
		}
		pos++
		end := strings.IndexByte(value[pos:], ')')
		if end < 0 {
			return Matrix{}, fmt.Errorf("svg: missing ')' in transform %q", value)
		}
		args, err := parseNumberList(value[pos : pos+end])
		if err != nil {
			return Matrix{}, fmt.Errorf("svg: %s transform: %w", name, err)
		}
		pos += end + 1

		var n Matrix
		switch name {
		case "matrix":
			if len(args) != 6 {
				return Matrix{}, fmt.Errorf("svg: matrix transform requires 6 values")
			}
			n = Matrix{A: args[0], B: args[1], C: args[2], D: args[3], E: args[4], F: args[5]}
		case "translate":
			if len(args) != 1 && len(args) != 2 {
				return Matrix{}, fmt.Errorf("svg: translate transform requires 1 or 2 values")
			}
			y := 0.0
			if len(args) == 2 {
				y = args[1]
			}
			n = translate(args[0], y)
		case "scale":
			if len(args) != 1 && len(args) != 2 {
				return Matrix{}, fmt.Errorf("svg: scale transform requires 1 or 2 values")
			}
			y := args[0]
			if len(args) == 2 {
				y = args[1]
			}
			n = scale(args[0], y)
		case "rotate":
			if len(args) != 1 && len(args) != 3 {
				return Matrix{}, fmt.Errorf("svg: rotate transform requires 1 or 3 values")
			}
			n = rotate(args[0] * math.Pi / 180)
			if len(args) == 3 {
				n = translate(args[1], args[2]).Mul(n).Mul(translate(-args[1], -args[2]))
			}
		case "skewx":
			if len(args) != 1 {
				return Matrix{}, fmt.Errorf("svg: skewX transform requires 1 value")
			}
			n = Matrix{A: 1, C: math.Tan(args[0] * math.Pi / 180), D: 1}
		case "skewy":
			if len(args) != 1 {
				return Matrix{}, fmt.Errorf("svg: skewY transform requires 1 value")
			}
			n = Matrix{A: 1, B: math.Tan(args[0] * math.Pi / 180), D: 1}
		default:
			return Matrix{}, fmt.Errorf("svg: unsupported transform %q", name)
		}
		m = m.Mul(n)
		if !m.finite() {
			return Matrix{}, fmt.Errorf("svg: non-finite transform %q", value)
		}
	}
}

type segmentKind uint8

const (
	moveTo segmentKind = iota
	lineTo
	quadTo
	cubicTo
	closePath
)

type segment struct {
	kind segmentKind
	p    [3]Point
}

// Path is compiled SVG path geometry.
type Path struct {
	segments []segment
}

func (p *Path) moveTo(v Point) {
	p.segments = append(p.segments, segment{kind: moveTo, p: [3]Point{v}})
}
func (p *Path) lineTo(v Point) {
	p.segments = append(p.segments, segment{kind: lineTo, p: [3]Point{v}})
}
func (p *Path) quadTo(c, v Point) {
	p.segments = append(p.segments, segment{kind: quadTo, p: [3]Point{c, v}})
}
func (p *Path) cubicTo(c1, c2, v Point) {
	p.segments = append(p.segments, segment{kind: cubicTo, p: [3]Point{c1, c2, v}})
}
func (p *Path) close()             { p.segments = append(p.segments, segment{kind: closePath}) }
func add(a, b Point) Point         { return Point{a.X + b.X, a.Y + b.Y} }
func sub(a, b Point) Point         { return Point{a.X - b.X, a.Y - b.Y} }
func mul(a Point, n float64) Point { return Point{a.X * n, a.Y * n} }
func midpoint(a, b Point) Point    { return Point{(a.X + b.X) / 2, (a.Y + b.Y) / 2} }
func distanceToLine(p, a, b Point) float64 {
	return math.Abs((b.X-a.X)*(a.Y-p.Y)-(a.X-p.X)*(b.Y-a.Y)) / math.Max(math.Hypot(b.X-a.X, b.Y-a.Y), 1e-15)
}
func almostEqual(a, b Point) bool { return math.Abs(a.X-b.X) < 1e-12 && math.Abs(a.Y-b.Y) < 1e-12 }
func pointOnEllipse(cx, cy, rx, ry, phi, t float64) Point {
	return Point{cx + rx*math.Cos(phi)*math.Cos(t) - ry*math.Sin(phi)*math.Sin(t), cy + rx*math.Sin(phi)*math.Cos(t) + ry*math.Cos(phi)*math.Sin(t)}
}

type contour struct {
	points []Point
	closed bool
}

func (p *Path) flatten(m Matrix, tolerance float64) []contour {
	if tolerance <= 0 {
		tolerance = 0.25
	}
	var out []contour
	var cur contour
	var here, start Point
	flush := func() {
		if len(cur.points) > 1 {
			out = append(out, cur)
		}
		cur = contour{}
	}
	appendPoint := func(v Point) {
		v = m.Apply(v)
		if len(cur.points) == 0 || !almostEqual(cur.points[len(cur.points)-1], v) {
			cur.points = append(cur.points, v)
		}
	}
	for _, s := range p.segments {
		switch s.kind {
		case moveTo:
			flush()
			here, start = s.p[0], s.p[0]
			appendPoint(here)
		case lineTo:
			if len(cur.points) == 0 {
				appendPoint(here)
			}
			here = s.p[0]
			appendPoint(here)
		case quadTo:
			if len(cur.points) == 0 {
				appendPoint(here)
			}
			p0, p1, p2 := m.Apply(here), m.Apply(s.p[0]), m.Apply(s.p[1])
			flattenQuad(&cur.points, p0, p1, p2, tolerance, 0)
			here = s.p[1]
		case cubicTo:
			if len(cur.points) == 0 {
				appendPoint(here)
			}
			p0, p1, p2, p3 := m.Apply(here), m.Apply(s.p[0]), m.Apply(s.p[1]), m.Apply(s.p[2])
			flattenCubic(&cur.points, p0, p1, p2, p3, tolerance, 0)
			here = s.p[2]
		case closePath:
			if len(cur.points) > 0 {
				appendPoint(start)
				cur.closed = true
			}
			here = start
			flush()
		}
	}
	flush()
	return out
}

func flattenQuad(dst *[]Point, p0, p1, p2 Point, tolerance float64, depth int) {
	polygonLength := math.Hypot(p1.X-p0.X, p1.Y-p0.Y) + math.Hypot(p2.X-p1.X, p2.Y-p1.Y)
	chordLength := math.Hypot(p2.X-p0.X, p2.Y-p0.Y)
	if depth >= 16 || polygonLength-chordLength <= tolerance {
		if len(*dst) == 0 || !almostEqual((*dst)[len(*dst)-1], p2) {
			*dst = append(*dst, p2)
		}
		return
	}
	a, b := midpoint(p0, p1), midpoint(p1, p2)
	c := midpoint(a, b)
	flattenQuad(dst, p0, a, c, tolerance, depth+1)
	flattenQuad(dst, c, b, p2, tolerance, depth+1)
}

func flattenCubic(dst *[]Point, p0, p1, p2, p3 Point, tolerance float64, depth int) {
	polygonLength := math.Hypot(p1.X-p0.X, p1.Y-p0.Y) + math.Hypot(p2.X-p1.X, p2.Y-p1.Y) + math.Hypot(p3.X-p2.X, p3.Y-p2.Y)
	chordLength := math.Hypot(p3.X-p0.X, p3.Y-p0.Y)
	if depth >= 16 || polygonLength-chordLength <= tolerance {
		if len(*dst) == 0 || !almostEqual((*dst)[len(*dst)-1], p3) {
			*dst = append(*dst, p3)
		}
		return
	}
	p01, p12, p23 := midpoint(p0, p1), midpoint(p1, p2), midpoint(p2, p3)
	p012, p123 := midpoint(p01, p12), midpoint(p12, p23)
	p0123 := midpoint(p012, p123)
	flattenCubic(dst, p0, p01, p012, p0123, tolerance, depth+1)
	flattenCubic(dst, p0123, p123, p23, p3, tolerance, depth+1)
}

type numberScanner struct {
	s   string
	pos int
}

func (s *numberScanner) skip() {
	for s.pos < len(s.s) && (s.s[s.pos] == ',' || unicode.IsSpace(rune(s.s[s.pos]))) {
		s.pos++
	}
}

func (s *numberScanner) hasNumber() bool {
	s.skip()
	if s.pos >= len(s.s) {
		return false
	}
	c := s.s[s.pos]
	return c == '+' || c == '-' || c == '.' || c >= '0' && c <= '9'
}

func (s *numberScanner) number() (float64, error) {
	s.skip()
	start := s.pos
	if s.pos < len(s.s) && (s.s[s.pos] == '+' || s.s[s.pos] == '-') {
		s.pos++
	}
	digits := false
	for s.pos < len(s.s) && s.s[s.pos] >= '0' && s.s[s.pos] <= '9' {
		digits = true
		s.pos++
	}
	if s.pos < len(s.s) && s.s[s.pos] == '.' {
		s.pos++
		for s.pos < len(s.s) && s.s[s.pos] >= '0' && s.s[s.pos] <= '9' {
			digits = true
			s.pos++
		}
	}
	if !digits {
		return 0, fmt.Errorf("expected number at byte %d", start)
	}
	if s.pos < len(s.s) && (s.s[s.pos] == 'e' || s.s[s.pos] == 'E') {
		exp := s.pos
		s.pos++
		if s.pos < len(s.s) && (s.s[s.pos] == '+' || s.s[s.pos] == '-') {
			s.pos++
		}
		expDigits := s.pos
		for s.pos < len(s.s) && s.s[s.pos] >= '0' && s.s[s.pos] <= '9' {
			s.pos++
		}
		if expDigits == s.pos {
			s.pos = exp
		}
	}
	v, err := strconv.ParseFloat(s.s[start:s.pos], 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Errorf("invalid number %q", s.s[start:s.pos])
	}
	return v, nil
}

func (s *numberScanner) flag() (bool, error) {
	s.skip()
	if s.pos >= len(s.s) || s.s[s.pos] != '0' && s.s[s.pos] != '1' {
		return false, fmt.Errorf("expected arc flag at byte %d", s.pos)
	}
	value := s.s[s.pos] == '1'
	s.pos++
	return value, nil
}

func parseNumberList(value string) ([]float64, error) {
	s := numberScanner{s: value}
	var out []float64
	for s.hasNumber() {
		v, err := s.number()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	s.skip()
	if s.pos != len(value) {
		return nil, fmt.Errorf("unexpected input at byte %d", s.pos)
	}
	return out, nil
}

// ParsePath compiles SVG path data.
func ParsePath(data string) (*Path, error) {
	s := numberScanner{s: data}
	p := &Path{}
	var cmd byte
	var here, start, cubicControl, quadControl Point
	var previous byte

	next := func() (float64, error) { return s.number() }
	pair := func(relative bool) (Point, error) {
		x, err := next()
		if err != nil {
			return Point{}, err
		}
		y, err := next()
		if err != nil {
			return Point{}, err
		}
		v := Point{x, y}
		if relative {
			v = add(here, v)
		}
		return v, nil
	}

	for {
		s.skip()
		if s.pos >= len(data) {
			break
		}
		if unicode.IsLetter(rune(data[s.pos])) {
			cmd = data[s.pos]
			s.pos++
		} else if cmd == 0 {
			return nil, fmt.Errorf("svg path: expected command at byte %d", s.pos)
		}
		lower := byte(unicode.ToLower(rune(cmd)))
		relative := cmd >= 'a' && cmd <= 'z'
		if len(p.segments) == 0 && lower != 'm' {
			return nil, fmt.Errorf("svg path: first command must be M or m")
		}
		if lower == 'z' {
			p.close()
			here = start
			previous = lower
			cmd = 0
			continue
		}
		if !s.hasNumber() {
			return nil, fmt.Errorf("svg path: command %c has no parameters", cmd)
		}

		switch lower {
		case 'm':
			v, err := pair(relative)
			if err != nil {
				return nil, fmt.Errorf("svg path M: %w", err)
			}
			p.moveTo(v)
			here, start = v, v
			previous = 'm'
			for s.hasNumber() {
				v, err = pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path M: %w", err)
				}
				p.lineTo(v)
				here = v
				previous = 'l'
			}
		case 'l':
			for s.hasNumber() {
				v, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path L: %w", err)
				}
				p.lineTo(v)
				here = v
				previous = lower
			}
		case 'h', 'v':
			for s.hasNumber() {
				v, err := next()
				if err != nil {
					return nil, fmt.Errorf("svg path %c: %w", cmd, err)
				}
				to := here
				if lower == 'h' {
					to.X = v
					if relative {
						to.X += here.X
					}
				} else {
					to.Y = v
					if relative {
						to.Y += here.Y
					}
				}
				p.lineTo(to)
				here = to
				previous = lower
			}
		case 'c':
			for s.hasNumber() {
				c1, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path C: %w", err)
				}
				c2, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path C: %w", err)
				}
				v, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path C: %w", err)
				}
				p.cubicTo(c1, c2, v)
				here, cubicControl = v, c2
				previous = lower
			}
		case 's':
			for s.hasNumber() {
				c1 := here
				if previous == 'c' || previous == 's' {
					c1 = sub(mul(here, 2), cubicControl)
				}
				c2, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path S: %w", err)
				}
				v, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path S: %w", err)
				}
				p.cubicTo(c1, c2, v)
				here, cubicControl = v, c2
				previous = lower
			}
		case 'q':
			for s.hasNumber() {
				c, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path Q: %w", err)
				}
				v, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path Q: %w", err)
				}
				p.quadTo(c, v)
				here, quadControl = v, c
				previous = lower
			}
		case 't':
			for s.hasNumber() {
				c := here
				if previous == 'q' || previous == 't' {
					c = sub(mul(here, 2), quadControl)
				}
				v, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path T: %w", err)
				}
				p.quadTo(c, v)
				here, quadControl = v, c
				previous = lower
			}
		case 'a':
			for s.hasNumber() {
				rx, err := next()
				if err != nil {
					return nil, fmt.Errorf("svg path A: %w", err)
				}
				ry, err := next()
				if err != nil {
					return nil, fmt.Errorf("svg path A: %w", err)
				}
				angle, err := next()
				if err != nil {
					return nil, fmt.Errorf("svg path A: %w", err)
				}
				large, err := s.flag()
				if err != nil {
					return nil, fmt.Errorf("svg path A: large-arc flag must be 0 or 1")
				}
				sweep, err := s.flag()
				if err != nil {
					return nil, fmt.Errorf("svg path A: sweep flag must be 0 or 1")
				}
				v, err := pair(relative)
				if err != nil {
					return nil, fmt.Errorf("svg path A: %w", err)
				}
				p.arcTo(here, v, rx, ry, angle, large, sweep)
				here = v
				previous = lower
			}
		default:
			return nil, fmt.Errorf("svg path: unsupported command %c", cmd)
		}
	}
	if len(p.segments) == 0 {
		return nil, fmt.Errorf("svg path: empty path")
	}
	return p, nil
}

func (p *Path) arcTo(from, to Point, rx, ry, angle float64, large, sweep bool) {
	if almostEqual(from, to) {
		return
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		p.lineTo(to)
		return
	}
	phi := math.Mod(angle, 360) * math.Pi / 180
	sinPhi, cosPhi := math.Sincos(phi)
	dx, dy := (from.X-to.X)/2, (from.Y-to.Y)/2
	x1p := cosPhi*dx + sinPhi*dy
	y1p := -sinPhi*dx + cosPhi*dy
	lambda := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry)
	if lambda > 1 {
		factor := math.Sqrt(lambda)
		rx *= factor
		ry *= factor
	}
	rx2, ry2 := rx*rx, ry*ry
	numerator := rx2*ry2 - rx2*y1p*y1p - ry2*x1p*x1p
	denominator := rx2*y1p*y1p + ry2*x1p*x1p
	coef := 0.0
	if denominator > 0 {
		coef = math.Sqrt(math.Max(0, numerator/denominator))
	}
	if large == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1p / ry
	cyp := coef * -ry * x1p / rx
	cx := cosPhi*cxp - sinPhi*cyp + (from.X+to.X)/2
	cy := sinPhi*cxp + cosPhi*cyp + (from.Y+to.Y)/2

	ux, uy := (x1p-cxp)/rx, (y1p-cyp)/ry
	vx, vy := (-x1p-cxp)/rx, (-y1p-cyp)/ry
	theta := math.Atan2(uy, ux)
	delta := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	} else if sweep && delta < 0 {
		delta += 2 * math.Pi
	}
	pieces := int(math.Ceil(math.Abs(delta) / (math.Pi / 2)))
	step := delta / float64(pieces)
	for i := 0; i < pieces; i++ {
		t1 := theta + float64(i)*step
		t2 := t1 + step
		alpha := 4.0 / 3.0 * math.Tan((t2-t1)/4)
		q1 := pointOnEllipse(cx, cy, rx, ry, phi, t1)
		q2 := pointOnEllipse(cx, cy, rx, ry, phi, t2)
		d1 := Point{-rx*cosPhi*math.Sin(t1) - ry*sinPhi*math.Cos(t1), -rx*sinPhi*math.Sin(t1) + ry*cosPhi*math.Cos(t1)}
		d2 := Point{-rx*cosPhi*math.Sin(t2) - ry*sinPhi*math.Cos(t2), -rx*sinPhi*math.Sin(t2) + ry*cosPhi*math.Cos(t2)}
		p.cubicTo(add(q1, mul(d1, alpha)), sub(q2, mul(d2, alpha)), q2)
	}
	// Avoid accumulated floating-point error at the endpoint.
	p.segments[len(p.segments)-1].p[2] = to
}
