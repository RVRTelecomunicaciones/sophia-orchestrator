package handlers

import "strconv"

// atoiDefault parses s as int, returning d on error or empty input.
func atoiDefault(s string, d int) int {
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}
