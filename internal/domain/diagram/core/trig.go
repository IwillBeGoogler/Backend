package core

import "math"

// sin and cos as V8 computes them (fdlibm), so the force and radial layouts
// land on the same coordinates as the TypeScript original. Arguments beyond
// 2^19*pi/2 fall back to the standard library.

func hiWord(x float64) int32 { return int32(math.Float64bits(x) >> 32) }

func kernelSin(x, y float64, iy int) float64 {
	const (
		s1 = -1.66666666666666324348e-01
		s2 = 8.33333333332248946124e-03
		s3 = -1.98412698298579493134e-04
		s4 = 2.75573137070700676789e-06
		s5 = -2.50507602534068634195e-08
		s6 = 1.58969099521155010221e-10
	)
	ix := hiWord(x) & 0x7fffffff
	if ix < 0x3e400000 && int(x) == 0 {
		return x
	}
	z := x * x
	v := z * x
	r := s2 + float64(z*(s3+float64(z*(s4+float64(z*(s5+float64(z*s6)))))))
	if iy == 0 {
		return x + float64(v*(s1+float64(z*r)))
	}
	return x - ((float64(z*(float64(0.5*y)-float64(v*r))) - y) - float64(v*s1))
}

func kernelCos(x, y float64) float64 {
	const (
		c1 = 4.16666666666666019037e-02
		c2 = -1.38888888888741095749e-03
		c3 = 2.48015872894767294178e-05
		c4 = -2.75573143513906633035e-07
		c5 = 2.08757232129817482790e-09
		c6 = -1.13596475577881948265e-11
	)
	ix := hiWord(x) & 0x7fffffff
	if ix < 0x3e400000 && int(x) == 0 {
		return 1
	}
	z := x * x
	r := float64(z * (c1 + float64(z*(c2+float64(z*(c3+float64(z*(c4+float64(z*(c5+float64(z*c6)))))))))))
	if ix < 0x3FD33333 {
		return 1 - (float64(0.5*z) - (float64(z*r) - float64(x*y)))
	}
	var qx float64
	if ix > 0x3fe90000 {
		qx = 0.28125
	} else {
		qx = math.Float64frombits(uint64(uint32(ix-0x00200000)) << 32)
	}
	hz := float64(0.5*z) - qx
	a := 1 - qx
	return a - (hz - (float64(z*r) - float64(x*y)))
}

// remPio2 returns n and the reduced argument (y0, y1); ok is false when x is
// too large for the medium-range reduction.
func remPio2(x float64) (n int, y0, y1 float64, ok bool) {
	const (
		invpio2 = 6.36619772367581382433e-01
		pio21   = 1.57079632673412561417e+00
		pio21t  = 6.07710050650619224932e-11
		pio22   = 6.07710050630396597660e-11
		pio22t  = 2.02226624879595063154e-21
		pio23   = 2.02226624871116645580e-21
		pio23t  = 8.47842766036889956997e-32
	)
	hx := hiWord(x)
	ix := hx & 0x7fffffff
	if ix < 0x4002d97c {
		if hx > 0 {
			z := x - pio21
			if ix != 0x3ff921fb {
				y0 = z - pio21t
				y1 = (z - y0) - pio21t
			} else {
				z -= pio22
				y0 = z - pio22t
				y1 = (z - y0) - pio22t
			}
			return 1, y0, y1, true
		}
		z := x + pio21
		if ix != 0x3ff921fb {
			y0 = z + pio21t
			y1 = (z - y0) + pio21t
		} else {
			z += pio22
			y0 = z + pio22t
			y1 = (z - y0) + pio22t
		}
		return -1, y0, y1, true
	}
	if ix <= 0x413921fb {
		t := math.Abs(x)
		n = int(float64(t*invpio2) + 0.5)
		fn := float64(n)
		r := t - float64(fn*pio21)
		w := fn * pio21t
		j := ix >> 20
		y0 = r - w
		i := j - ((hiWord(y0) >> 20) & 0x7ff)
		if i > 16 {
			t = r
			w = fn * pio22
			r = t - w
			w = float64(fn*pio22t) - ((t - r) - w)
			y0 = r - w
			i = j - ((hiWord(y0) >> 20) & 0x7ff)
			if i > 49 {
				t = r
				w = fn * pio23
				r = t - w
				w = float64(fn*pio23t) - ((t - r) - w)
				y0 = r - w
			}
		}
		y1 = (r - y0) - w
		if hx < 0 {
			return -n, -y0, -y1, true
		}
		return n, y0, y1, true
	}
	return 0, 0, 0, false
}

func jsSin(x float64) float64 {
	ix := hiWord(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelSin(x, 0, 0)
	}
	if ix >= 0x7ff00000 {
		return math.NaN()
	}
	n, y0, y1, ok := remPio2(x)
	if !ok {
		return math.Sin(x)
	}
	switch n & 3 {
	case 0:
		return kernelSin(y0, y1, 1)
	case 1:
		return kernelCos(y0, y1)
	case 2:
		return -kernelSin(y0, y1, 1)
	}
	return -kernelCos(y0, y1)
}

func jsCos(x float64) float64 {
	ix := hiWord(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelCos(x, 0)
	}
	if ix >= 0x7ff00000 {
		return math.NaN()
	}
	n, y0, y1, ok := remPio2(x)
	if !ok {
		return math.Cos(x)
	}
	switch n & 3 {
	case 0:
		return kernelCos(y0, y1)
	case 1:
		return -kernelSin(y0, y1, 1)
	case 2:
		return -kernelCos(y0, y1)
	}
	return kernelSin(y0, y1, 1)
}
