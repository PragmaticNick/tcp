package tcp

func isBetweenWrapped(start, x, end uint32) bool {
	if start < end {
		return x > start && x < end
	} else {
		return x > start || x < end
	}
}

func isStateSynchronized(state int) bool {
	if state == SynReceived {
		return false
	}

	if state == Established || state == FinWait1 || state == FinWait2 || state == Closing {
		return true
	}
	return false
}
