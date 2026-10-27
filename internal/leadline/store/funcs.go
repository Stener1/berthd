package store

import (
	"database/sql/driver"
	"errors"
	"math"
	"sort"

	"modernc.org/sqlite"
)

// SQLite has no percentile, no "the value on the earliest row" and no
// logarithm that can be relied on, and the breakdowns need all three. They
// are given to it here, in Go, once for every connection the process opens:
//
//	ll_quantile(x, q)   aggregate: the q-th quantile of x, between the two
//	                    nearest values where it falls between (DuckDB's
//	                    quantile_cont); NULL of nothing
//	ll_first(v, k)      aggregate: v on the row with the smallest k
//	ll_last(v, k)       aggregate: v on the row with the largest k
//	ll_band(ns, k)      the latency band a duration falls in; see band
//
// FILTER and GROUP BY work on them as on any aggregate.
func init() {
	sqlite.MustRegisterFunction("ll_quantile", &sqlite.FunctionImpl{NArgs: 2, Deterministic: true,
		MakeAggregate: func(sqlite.FunctionContext) (sqlite.AggregateFunction, error) { return &quantile{}, nil }})
	sqlite.MustRegisterFunction("ll_first", &sqlite.FunctionImpl{NArgs: 2, Deterministic: true,
		MakeAggregate: func(sqlite.FunctionContext) (sqlite.AggregateFunction, error) { return &pick{}, nil }})
	sqlite.MustRegisterFunction("ll_last", &sqlite.FunctionImpl{NArgs: 2, Deterministic: true,
		MakeAggregate: func(sqlite.FunctionContext) (sqlite.AggregateFunction, error) { return &pick{last: true}, nil }})
	sqlite.MustRegisterFunction("ll_band", &sqlite.FunctionImpl{NArgs: 2, Deterministic: true,
		Scalar: func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			ns, ok1 := number(args[0])
			k, ok2 := number(args[1])
			if !ok1 || !ok2 {
				return nil, nil
			}
			return int64(band(int64(ns), int(k))), nil
		}})
}

var errNotWindow = errors.New("not a window function")

func number(v driver.Value) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

type quantile struct {
	q    float64
	vals []float64
}

func (a *quantile) Step(_ *sqlite.FunctionContext, args []driver.Value) error {
	v, ok := number(args[0])
	if !ok {
		return nil // NULL, as every aggregate skips it
	}
	if q, ok := number(args[1]); ok {
		a.q = q
	}
	a.vals = append(a.vals, v)
	return nil
}

func (a *quantile) WindowInverse(*sqlite.FunctionContext, []driver.Value) error { return errNotWindow }

func (a *quantile) WindowValue(*sqlite.FunctionContext) (driver.Value, error) {
	if len(a.vals) == 0 {
		return nil, nil
	}
	return quantileOf(a.vals, a.q), nil
}

func (a *quantile) Final(*sqlite.FunctionContext) { a.vals = nil }

// quantileOf sorts vals and reads the q-th quantile off them.
func quantileOf(vals []float64, q float64) float64 {
	sort.Float64s(vals)
	at := min(max(q, 0), 1) * float64(len(vals)-1)
	lo := int(math.Floor(at))
	hi := min(lo+1, len(vals)-1)
	return vals[lo] + (vals[hi]-vals[lo])*(at-float64(lo))
}

// pick keeps the value that came with the smallest key, or the largest.
// Among rows of one key the first one seen is kept.
type pick struct {
	last  bool
	have  bool
	key   driver.Value
	value driver.Value
}

func (a *pick) Step(_ *sqlite.FunctionContext, args []driver.Value) error {
	if _, ok := number(args[1]); !ok || args[0] == nil {
		return nil
	}
	if c := compare(args[1], a.key); !a.have || (a.last && c > 0) || (!a.last && c < 0) {
		a.have, a.key, a.value = true, args[1], args[0]
	}
	return nil
}

// compare orders two numbers. Whole numbers are compared as such: a time in
// nanoseconds has more digits than a float keeps.
func compare(a, b driver.Value) int {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
	}
	x, _ := number(a)
	y, _ := number(b)
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

func (a *pick) WindowInverse(*sqlite.FunctionContext, []driver.Value) error { return errNotWindow }

func (a *pick) WindowValue(*sqlite.FunctionContext) (driver.Value, error) {
	if !a.have {
		return nil, nil
	}
	return a.value, nil
}

func (a *pick) Final(*sqlite.FunctionContext) {}

// band is the latency band a duration falls in. Bands are log-spaced: band
// i holds durations from 10^(i/k) to 10^((i+1)/k) milliseconds, k bands to
// the decade. Durations are clamped to 1µs..1h so one broken span can't
// stretch a chart to nothing.
func band(ns int64, k int) int {
	ns = min(max(ns, 1000), 3_600_000_000_000)
	return int(math.Floor(math.Log10(float64(ns)/1e6) * float64(k)))
}
