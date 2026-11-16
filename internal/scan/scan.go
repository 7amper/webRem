package scan

import (
	"math"

	"rockpi-scan/internal/config"
)

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Build формирует маршрут построчного сканирования (змейкой).
func Build(c config.Scan) []Point {
	step := c.Step
	if step <= 0 {
		step = 1
	}

	nx := int(math.Floor(c.RangeX/step + 1e-9))
	ny := int(math.Floor(c.RangeY/step + 1e-9))
	if nx < 1 {
		nx = 1
	}
	if ny < 1 {
		ny = 1
	}

	pts := make([]Point, 0, (nx+1)*(ny+1))

	if c.Direction == "across" {
		// строки вдоль оси Y (поперёк трубы)
		for i := 0; i <= nx; i++ {
			x := c.StartX + float64(i)*step
			if i%2 == 0 {
				for j := 0; j <= ny; j++ {
					pts = append(pts, Point{X: x, Y: c.StartY + float64(j)*step})
				}
			} else {
				for j := ny; j >= 0; j-- {
					pts = append(pts, Point{X: x, Y: c.StartY + float64(j)*step})
				}
			}
		}
		return pts
	}

	// "along": строки вдоль оси X
	for j := 0; j <= ny; j++ {
		y := c.StartY + float64(j)*step
		if j%2 == 0 {
			for i := 0; i <= nx; i++ {
				pts = append(pts, Point{X: c.StartX + float64(i)*step, Y: y})
			}
		} else {
			for i := nx; i >= 0; i-- {
				pts = append(pts, Point{X: c.StartX + float64(i)*step, Y: y})
			}
		}
	}
	return pts
}