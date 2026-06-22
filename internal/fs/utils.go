package fs

func safeInt64ToUint64(n int64) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n)
}

func safeIntToUint32(n int) uint32 {
	if n < 0 {
		return 0
	}
	if uint64(n) > uint64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(n)
}
