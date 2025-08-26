package perf

import "errors"

// Summarize returns min, max, avg; zeroed if empty input
func Summarize(vals []float64) (min, max, avg float64) {
	if len(vals) == 0 {
		return 0, 0, 0
	}
	min, max, sum := vals[0], vals[0], 0.0
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	avg = sum / float64(len(vals))
	return
}

// EnsureCSVHeader returns the header to be written if file is missing
func EnsureCSVHeader(path, header string, stat func(string) (interface{}, error), writeFile func(string, []byte, uint32) error) {
	if _, err := stat(path); errors.Is(err, errors.New("file does not exist")) {
		_ = writeFile(path, []byte(header), 0644)
	}
}
