package microspace

import "sort"

// Index describes a spatial index that can look
// up a point's nearest neighbors.
type Index interface {
	// NearestN returns up the `n` nearest neighbors of the point, with
	// a `max` search distance. `n` May be set to -1 to search for all
	// neighbors in the distance.8
	NearestN(p *Point, n int, max float32) []*Point
	// Points returns all points contained in the spatial index.
	Points() []*Point
}

// axisPoint is used for internal recordkeeping of points within an axis.
// It's a pair of the point and the value of that point's coordinate on
// the related axis.
type axisPoint struct {
	p     *Point
	value float32
}

// axisPointList implements sort.Interface
type axisPointList []axisPoint

// Len implements sort.Interface.Len
func (a axisPointList) Len() int {
	return len(a)
}

// Less implements sort.Interface.Less
func (a axisPointList) Less(i, j int) bool {
	return a[i].value < a[j].value
}

// Swap implements sort.Interface.Swap
func (a axisPointList) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

// axis stores a sorted set of points along a one-dimensional line.
type axis struct {
	data  axisPointList
	value func(*Point) float32

	sorted  bool
	indexed map[*Point]int
}

// newAxis returns an axis created with the provided capacity. It is assumed
// that the axis will be filled with exactly `capacity` points before
// any other operations are done on it.
func newAxis(capacity uint, value func(*Point) float32) *axis {
	return &axis{
		data:  make([]axisPoint, 0, capacity),
		value: func(p *Point) float32 { return p.Y },
	}
}

// IndexFor returns the index of the point on the axis. It's assumed that the
// point will exist in the axis.
func (a *axis) IndexFor(p *Point) int {
	if !a.sorted {
		a.runSort()
	}

	return a.indexed[p]
}

// runSort sorts the data points stored in the axis and generates an index
// for them.
func (a *axis) runSort() {
	sort.Sort(a.data)

	a.indexed = map[*Point]int{}
	for i, pt := range a.data {
		a.indexed[pt.p] = i
	}

	a.sorted = true
}

// ValueFor returns the point's coordinate on that axis.
func (a *axis) ValueFor(p *Point) float32 {
	return a.value(p)
}

// Insert adds a new point to the axis.
func (a *axis) Insert(p *Point) {
	if a.sorted {
		panic("Cannot add items to the index after starting to use it.")
	}

	a.data = append(a.data, axisPoint{p: p, value: a.value(p)})
}

type Axdex struct {
	axis   *axis
	points []*Point
}

// NewAxdex returns a new axis-based index with the provided capacity.
// It's assumed that you will insert exactly `capacity` points before
// running queries against the index.
func NewAxdex(capacity uint) *Axdex {
	a := &Axdex{
		axis: newAxis(capacity, func(p *Point) float32 { return p.X }),
	}

	return a
}

var _ Index = new(Axdex)

// Insert implements Index.Insert
func (a *Axdex) Insert(p *Point) {
	a.axis.Insert(p)
	a.points = append(a.points, p)
}

// Points implements Index.Points
func (a *Axdex) Points() []*Point {
	return a.points
}

type axResults struct {
	src   *Point
	data  []*Point
	worst float32
	count int
}

// Viable returns true if the provided value could possible be a coordinate
// of a nearest neighbor with coordinate src.
func (a *axResults) Viable(p *Point) (viable bool, distance float32) {
	d := p.DistanceToSqr(a.src)
	if a.data[a.count-1] == nil {
		return true, d
	}

	return d < a.worst, d
}

// HasPotential returns true if the difference between the center point and
// another point, given as delta, is less than the provided max and if it
// could possibly yield a viable point. Once this returns false for an axis
// points "further out" on that axis will not have potential either.
func (a *axResults) HasPotential(delta, max float32) bool {
	if delta > max || -delta > max {
		return false
	}

	if a.data[a.count-1] == nil {
		return true
	}

	return delta*delta < a.worst
}

// GetResult returns a list of results from the list. It will returns as many
// non-nil results as it can, up to the provided count.
func (a *axResults) GetResult() []*Point {
	var i int
	for i < a.count && a.data[i] != nil {
		i++
	}

	return a.data[:i]
}

// Attempts to insert the point into the results.
func (a *axResults) Insert(p *Point) {
	for i := 0; i < a.count; i++ {
		if a.data[i] == p {
			return
		}

		if a.data[i] == nil {
			a.data[i] = p
			break
		}

		if a.src.DistanceToSqr(p) < a.src.DistanceToSqr(a.data[i]) {
			copy(a.data[i+1:], a.data[i:])
			a.data[i] = p
			break
		}
	}

	if a.data[a.count-1] != nil {
		a.worst = a.data[a.count-1].DistanceToSqr(a.src)
	}
}

// NearestN returns up the `n` nearest neighbors of the point, with a `max`
// search distance. It's assumed that p is in the index!
func (a *Axdex) NearestN(p *Point, n int, max float32) []*Point {
	if n == -1 {
		n = len(a.points)
	}

	results := &axResults{src: p, data: make([]*Point, n), count: n}
	results.Insert(p)

	// Warning: logic ahead!
	// The general algorithm is this. We loop through the axis, starting
	// at the point in the sorted list of points on that axis and expanding
	// outwards. As we expand, we look for points that are near to the
	// center point, and keep track of the n nearest.
	idx := a.axis.IndexFor(p)
	var (
		size  = len(a.axis.data)
		left  = idx - 1
		right = idx + 1
		value = a.axis.ValueFor(p)
	)

	// At each of these loops, we expand the `left` and/or the `right`
	// outwards. We do this until the 'distance' along the axis of each
	// the left and right pointer is greater than the worst distance
	// in our results. When this point is reached it's impossible that
	// we'll find any more viable points along this axis, so we exit.
	//
	// The `left` pointer is set to -1 when it's out of potential points.
	// The `right` pointer is set to len(axis.data) when it's out of points.
	for {
		var (
			// leftP/rightP are the point and axis position associated with
			// that point, from the left/right index.
			leftP  axisPoint
			rightP axisPoint

			// Viable is set to true if the point at that distance is
			// closer than the worst point in the results set.
			leftViable  = false
			rightViable = false

			// Euclidean distance squared of the provided point to the
			// center point.
			leftDistance  = float32(0)
			rightDistance = float32(0)
		)

		if left >= 0 { // if we might have something to the left of the point
			leftP = a.axis.data[left]
			leftViable, leftDistance = results.Viable(leftP.p)

			// This point wasn't viable, but we might have something
			// further on! Decrement the left pointer.
			if !leftViable {
				left--
			}
		}

		if right < size { // if we might have something to the left of the point
			rightP = a.axis.data[right]
			rightViable, rightDistance = results.Viable(rightP.p)

			// This point wasn't viable, but we might have something
			// further on! Increment the right pointer.
			if !rightViable {
				right++
			}
		}

		// At this point we know whether each point is viable and its
		// distance to the center point. Chose either the only viable
		// point, or the point closer to the center, and insert it in
		// the results.
		if leftViable && (!rightViable || leftDistance < rightDistance) {
			results.Insert(leftP.p)
			left--
		} else if rightViable {
			results.Insert(rightP.p)
			right++
		}

		// Now, we've updated the left and right pointers to the next
		// position. We check to see if either direction has the
		// potential to contain more viable points. If not,
		// return from the loop.
		leftPotential := left >= 0 && results.HasPotential(value-leftP.value, max)
		rightPotential := right < size && results.HasPotential(value-rightP.value, max)
		if !(leftPotential || rightPotential) {
			break
		}

		// For directions that don't have potential, set them to their
		// terminated values so that we don't have to keep calculating
		// distances for them.
		if !leftPotential {
			left = -1
		}
		if !rightPotential {
			right = size
		}
	}

	return results.GetResult()
}
