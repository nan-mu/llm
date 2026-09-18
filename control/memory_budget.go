package control

// Theoretical resident set sizes (MB) for loaded models. Unloaded models omit memory_mb.
var memoryBudgetMB = map[string]int64{
	"translategemma-12b-it-6bit": 9560,
	"fun-asr-nano-2512-q8_0":     1045,
	"HY-MT2-7B-Q8_0":             7981,
	"Hy-MT2-7B-8bit":             7973,
}

func theoreticalMemoryMB(modelID string) (int64, bool) {
	mb, ok := memoryBudgetMB[modelID]
	return mb, ok
}
