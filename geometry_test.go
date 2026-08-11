package coolsvg

import (
	"math"
	"testing"
)

func TestParsePathSupportsSVGCommands(t *testing.T) {
	t.Parallel()

	path, err := ParsePath("M 1 2 l 3 0 h 2 v 3 c 1 0 2 1 3 1 s 2 1 3 0 q 1 -1 2 0 t 2 0 a 3 2 30 0 1 4 5 z")
	if err != nil {
		t.Fatal(err)
	}
	contours := path.flatten(Identity, 0.1)
	if len(contours) != 1 {
		t.Fatalf("got %d contours, want 1", len(contours))
	}
	if !contours[0].closed {
		t.Fatal("path was not closed")
	}
	got := contours[0].points[len(contours[0].points)-1]
	if got != (Point{X: 1, Y: 2}) {
		t.Fatalf("close point = %+v, want {1 2}", got)
	}
}

func TestParsePathRejectsInvalidArcFlag(t *testing.T) {
	t.Parallel()

	if _, err := ParsePath("M0 0 A10 10 0 2 0 20 20"); err == nil {
		t.Fatal("ParsePath accepted an invalid large-arc flag")
	}
}

func TestTransformOrderAndRotationCenter(t *testing.T) {
	t.Parallel()

	m, err := parseTransform("translate(10 20) scale(2) rotate(90, 1, 1)")
	if err != nil {
		t.Fatal(err)
	}
	got := m.Apply(Point{X: 2, Y: 1})
	assertNear(t, got.X, 12)
	assertNear(t, got.Y, 24)
}

func TestArcEndsAtRequestedPoint(t *testing.T) {
	t.Parallel()

	path, err := ParsePath("M 0 0 A 40 20 35 1 1 80 10")
	if err != nil {
		t.Fatal(err)
	}
	contours := path.flatten(Identity, 0.05)
	got := contours[0].points[len(contours[0].points)-1]
	assertNear(t, got.X, 80)
	assertNear(t, got.Y, 10)
}

func TestPathMustBeginWithMove(t *testing.T) {
	t.Parallel()

	if _, err := ParsePath("L 1 1"); err == nil {
		t.Fatal("ParsePath accepted a path that does not begin with moveto")
	}
}

func TestArcAcceptsPackedFlags(t *testing.T) {
	t.Parallel()

	if _, err := ParsePath("M0 0A10 10 0 0110 10"); err != nil {
		t.Fatalf("ParsePath rejected packed arc flags: %v", err)
	}
	if _, err := ParsePath("M0 0A10 10 0 0.0 10 10"); err == nil {
		t.Fatal("ParsePath accepted a non-grammar arc flag")
	}
}

func TestReversingCurveIsNotFlattenedAway(t *testing.T) {
	t.Parallel()

	path, err := ParsePath("M0 0 Q100 0 0 0")
	if err != nil {
		t.Fatal(err)
	}
	points := path.flatten(Identity, 0.1)[0].points
	if len(points) < 3 {
		t.Fatalf("reversing curve flattened to %d points", len(points))
	}
}

func TestCommandAfterCloseStartsNewContour(t *testing.T) {
	t.Parallel()

	path, err := ParsePath("M0 0H10V10Z L20 20")
	if err != nil {
		t.Fatal(err)
	}
	contours := path.flatten(Identity, 0.1)
	if len(contours) != 2 || !contours[0].closed || contours[1].closed {
		t.Fatalf("contours = %#v", contours)
	}
	if contours[1].points[0] != (Point{}) || contours[1].points[1] != (Point{20, 20}) {
		t.Fatalf("post-close contour = %#v", contours[1].points)
	}
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %g, want %g", got, want)
	}
}
