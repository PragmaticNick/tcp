package tcp

func isBetweenWrapped(start, x, end uint32) bool {
	if start < end {
		return x > start && x < end
	} else {
		return x > start || x < end
	}
}
