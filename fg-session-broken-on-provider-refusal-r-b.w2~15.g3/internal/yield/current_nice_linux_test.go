package yield

// currentNice is the calling thread's nice, calling threadNice(0) to undo Linux's
// 20-minus-nice offset across getpriority.
func currentNice() (int, error) { return threadNice(0) }
