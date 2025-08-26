package plan

import "math"

// CalculateBatchSize returns exponential batch size for 1-indexed batchNumber.
func CalculateBatchSize(initial, factor, batchNumber int) int {
	if batchNumber <= 0 {
		return 1
	}
	f := float64(initial) * math.Pow(float64(factor), float64(batchNumber-1))
	if f < 1 {
		return 1
	}
	return int(f + 0.00001)
}

// CalculateBatchesPlan produces a plan of [batchNumber, batchSize] pairs totaling up to maxNodes
func CalculateBatchesPlan(initial, factor, maxNodes, maxBatches int) [][2]int {
	current := 0
	var p [][2]int
	for batch := 1; current < maxNodes && batch <= maxBatches; batch++ {
		size := CalculateBatchSize(initial, factor, batch)
		if current+size > maxNodes {
			size = maxNodes - current
		}
		p = append(p, [2]int{batch, size})
		current += size
	}
	return p
}
