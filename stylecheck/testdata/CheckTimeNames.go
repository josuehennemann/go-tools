// Package pkg ...
package pkg

import "time"

type T1 struct {
	aMS     int
	B       time.Duration
	BMillis time.Duration // MATCH "don't use unit-specific suffix"
}

func fn1(a, b, cMS time.Duration) { // MATCH "don't use unit-specific suffix"
	var x time.Duration
	var xMS time.Duration    // MATCH "don't use unit-specific suffix"
	var y, yMS time.Duration // MATCH "don't use unit-specific suffix"
	_, _, _, _ = x, xMS, y, yMS
}
