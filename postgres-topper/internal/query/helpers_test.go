package query

import (
	"math"
	"time"
)

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func nan() float64 { return math.NaN() }
