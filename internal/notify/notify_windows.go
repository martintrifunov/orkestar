package notify

// resolve returns an empty notifier: Windows says it cannot post a
// notification rather than shipping a toast that silently fails.
func resolve() notifier { return notifier{} }
